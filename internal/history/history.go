package history

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/prayjofir/anitr-cli/internal/models"
	"github.com/prayjofir/anitr-cli/internal/player"
	"github.com/prayjofir/anitr-cli/internal/utils"
)

// getHistoryPath cross-platform olarak history.json yolunu döndürür
func getHistoryPath() (string, error) {
	historyDir := utils.ConfigDir()
	if err := os.MkdirAll(historyDir, 0o755); err != nil {
		return "", fmt.Errorf("history klasörü oluşturulamadı: %w", err)
	}
	return filepath.Join(historyDir, "history.json"), nil
}

// GetHistoryPath history.json dosyasının tam yolunu döner
func GetHistoryPath() (string, error) {
	return getHistoryPath()
}

// ReadAnimeHistory history.json'u okur, yoksa yeni oluşturur
func ReadAnimeHistory() (models.AnimeHistory, error) {
	path, err := getHistoryPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(models.AnimeHistory), nil
		}
		return nil, fmt.Errorf("history okunamadı: %w", err)
	}
	var history models.AnimeHistory
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, fmt.Errorf("history parse edilemedi: %w", err)
	}
	return history, nil
}

// WriteAnimeHistory history.json'u yazar
func WriteAnimeHistory(history models.AnimeHistory) error {
	path, err := getHistoryPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("history serialize edilemedi: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("history yazılamadı: %w", err)
	}
	return nil
}

// DeleteAnimeHistory — belirtilen animeni geçmişten siler
func DeleteAnimeHistory(source, animeName string) error {
	hist, err := ReadAnimeHistory()
	if err != nil {
		return err
	}
	if sourceData, ok := hist[source]; ok {
		delete(sourceData, animeName)
		hist[source] = sourceData
	}
	return WriteAnimeHistory(hist)
}

// trackPollInterval — MPV pozisyon sorgulama aralığı (testlerde kısaltılabilir)
var trackPollInterval = 5 * time.Second

// maxConsecutiveIPErr — üst üste bu kadar IPC hatası olursa MPV ölmüş sayılır
const maxConsecutiveIPErr = 12

// duplicatePositionEpsilonSec — pozisyon bu kadar değişmediyse diske tekrar yazılmaz
// (duraklatma / tekrar eden event'lerde gereksiz yazmayı önler)
const duplicatePositionEpsilonSec = 1.0

// UpdateAnimeHistory — MPV oturumu boyunca pozisyonu ve tamamlanma durumunu kaydeder.
//
//   - Her trackPollInterval'de bir `time-pos` okunur ve diske yazılır.
//   - Anime bazlı kayıt history.json'a, bölüm bazlı ilerleme progress.json'a yazılır
//     (animeKey + episodeKey ile; kaynak üst anahtarı sayesinde kaynaklar karışmaz).
//   - İzlenme %90'ı geçtiyse bölüm "bitti" (IsFinished=true) olarak işaretlenir ve
//     bölüm ilerlemesi progress.json'dan temizlenir.
//   - Süre bilinmiyorsa (canlı yayın) pozisyon kaydedilir ama asla "bitti" sayılmaz.
//   - IPC kesintisi bir süre tolere edilir; üst üste maxConsecutiveIPErr hata olursa çıkılır.
//   - Pozisyon değişmediyse (duraklatma) diske yazılmaz.
//   - totalEpisodeCount: o anki toplam bölüm sayısı (yeni bölüm tespiti için).
func UpdateAnimeHistory(
	socketPath, source, animeName, episodeName, animeId string,
	episodeIndex, totalEpisodeCount int,
	isMovie bool,
	animeKey, episodeKey string,
	logserv *models.LogServ,
) {
	saveHistory := func(positionSec *float64, isFinished bool) {
		hist, err := ReadAnimeHistory()
		if err != nil {
			utils.LogError(logserv, err)
			return
		}
		sourceEntry, ok := hist[source]
		if !ok {
			sourceEntry = make(map[string]models.AnimeHistoryEntry)
		}
		now := time.Now()
		sourceEntry[animeName] = models.AnimeHistoryEntry{
			LastEpisodeIdx:    &episodeIndex,
			LastEpisodeName:   episodeName,
			AnimeId:           &animeId,
			LastWatched:       &now,
			LastPositionSec:   positionSec,
			IsFinished:        isFinished,
			TotalEpisodeCount: totalEpisodeCount,
			IsMovie:           isMovie,
		}
		hist[source] = sourceEntry
		if err := WriteAnimeHistory(hist); err != nil {
			utils.LogError(logserv, err)
		}
	}

	// Bölüm bazlı ilerleme kaydı — yazma hatası izlemeyi durdurmaz, loglanır ve
	// bir sonraki tick'te tekrar denenir.
	saveProgress := func(pos, dur float64) {
		if episodeKey == "" {
			return
		}
		if err := SaveEpisodeProgress(source, animeKey, episodeKey, pos, dur); err != nil {
			utils.LogError(logserv, err)
		}
	}
	clearProgress := func() {
		if episodeKey == "" {
			return
		}
		if err := ClearEpisodeProgress(source, animeKey, episodeKey); err != nil {
			utils.LogError(logserv, err)
		}
	}

	// İlk kayıt — MPV başlar başlamaz
	saveHistory(nil, false)

	ticker := time.NewTicker(trackPollInterval)
	defer ticker.Stop()

	var lastKnownPos *float64
	var lastKnownDur float64
	var lastSavedPos *float64 // tekrar eden event'leri elemek için
	lastKnownFinished := false
	consecErr := 0

	for range ticker.C {
		if !player.IsMPVRunning(socketPath) {
			break
		}

		timePosVal, err1 := player.MPVSendCommand(socketPath, []interface{}{"get_property", "time-pos"})
		durationVal, err2 := player.MPVSendCommand(socketPath, []interface{}{"get_property", "duration"})
		if err1 != nil || err2 != nil {
			// IPC kesintisi — kısa kesintileri tolere et, sürekli ise çık
			consecErr++
			if consecErr >= maxConsecutiveIPErr {
				break
			}
			continue
		}
		consecErr = 0

		timePos, ok1 := timePosVal.(float64)
		if !ok1 {
			continue
		}
		duration, ok2 := durationVal.(float64)

		if !ok2 || duration <= 0 {
			// Canlı yayın / bilinmeyen süre — pozisyonu kaydet ama asla "bitti" sayma
			if lastSavedPos == nil || math.Abs(timePos-*lastSavedPos) >= duplicatePositionEpsilonSec {
				pos := timePos
				lastKnownPos = &pos
				lastKnownDur = 0
				lastSavedPos = &pos
				saveHistory(lastKnownPos, false)
				saveProgress(pos, 0)
			}
			continue
		}

		progress := timePos / duration
		if progress >= 0.9 {
			// Bölüm tamamlandı — pozisyonu sıfırla, bitti olarak işaretle,
			// bölüm ilerlemesini temizle
			saveHistory(nil, true)
			clearProgress()
			lastKnownFinished = true
			break
		}

		// Tekrar eden event (duraklatma vb.) — pozisyon değişmediyse diske yazma
		if lastSavedPos != nil && math.Abs(timePos-*lastSavedPos) < duplicatePositionEpsilonSec {
			continue
		}
		pos := timePos
		lastKnownPos = &pos
		lastKnownDur = duration
		lastSavedPos = &pos
		saveHistory(lastKnownPos, false)
		saveProgress(pos, duration)
	}

	// MPV kapandı — henüz bitmemişse son bilinen pozisyonu kaydet
	if !lastKnownFinished {
		saveHistory(lastKnownPos, false)
		if lastKnownPos != nil {
			saveProgress(*lastKnownPos, lastKnownDur)
		}
	}
}
