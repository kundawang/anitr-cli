package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/prayjofir/anitr-cli/internal/utils"
)

// EpisodeProgress, tek bir bölüm için kaydedilmiş izleme ilerlemesini tutar.
type EpisodeProgress struct {
	PositionSec float64   `json:"positionSec"`
	DurationSec float64   `json:"durationSec,omitempty"` // 0: canlı yayın / bilinmeyen süre
	UpdatedAt   time.Time `json:"updatedAt"`
}

// ProgressFile, progress.json'un yapısı:
// source -> animeKey -> episodeKey -> ilerleme.
// Kaynak üst anahtar olduğu için farklı kaynaklardaki aynı isimli bölümler karışmaz.
type ProgressFile map[string]map[string]map[string]EpisodeProgress

// progressMu, progress.json üzerindeki oku-değiştir-yaz işlemlerini seriler.
var progressMu sync.Mutex

// getProgressPath cross-platform olarak progress.json yolunu döndürür
func getProgressPath() (string, error) {
	progressDir := utils.ConfigDir()
	if err := os.MkdirAll(progressDir, 0o755); err != nil {
		return "", fmt.Errorf("progress klasörü oluşturulamadı: %w", err)
	}
	return filepath.Join(progressDir, "progress.json"), nil
}

// GetProgressPath progress.json dosyasının tam yolunu döner
func GetProgressPath() (string, error) {
	return getProgressPath()
}

// ReadProgress progress.json'u okur.
//   - Dosya yoksa boş kayıt döner.
//   - Dosya bozuksa (parse edilemiyorsa) progress.json.bak yedeğine taşınır
//     ve boş kayıt döner — uygulama sıfırdan temiz bir dosyayla devam eder.
func ReadProgress() (ProgressFile, error) {
	path, err := getProgressPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(ProgressFile), nil
		}
		return nil, fmt.Errorf("progress okunamadı: %w", err)
	}
	if len(data) == 0 {
		return make(ProgressFile), nil
	}
	var pf ProgressFile
	if err := json.Unmarshal(data, &pf); err != nil {
		// Bozuk dosya — yedeğe taşı (taşınamazsa sil) ve sıfırdan başla
		bak := path + ".bak"
		_ = os.Remove(bak)
		if rErr := os.Rename(path, bak); rErr != nil {
			_ = os.Remove(path)
		}
		return make(ProgressFile), nil
	}
	if pf == nil {
		pf = make(ProgressFile)
	}
	return pf, nil
}

// WriteProgress progress.json'u atomik olarak yazar (tmp dosya + rename).
// Yazma hatası çağırana hata olarak döner; eski dosya korunur.
func WriteProgress(pf ProgressFile) error {
	path, err := getProgressPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("progress serialize edilemedi: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("progress yazılamadı: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("progress kaydedilemedi: %w", err)
	}
	return nil
}

// SaveEpisodeProgress, tek bir bölümün pozisyonunu source+anime+episode anahtarıyla kaydeder.
// durationSec bilinmiyorsa (canlı yayın) 0 verilir.
func SaveEpisodeProgress(source, animeKey, episodeKey string, positionSec, durationSec float64) error {
	progressMu.Lock()
	defer progressMu.Unlock()

	pf, err := ReadProgress()
	if err != nil {
		return err
	}
	src, ok := pf[source]
	if !ok {
		src = make(map[string]map[string]EpisodeProgress)
	}
	anime, ok := src[animeKey]
	if !ok {
		anime = make(map[string]EpisodeProgress)
	}
	anime[episodeKey] = EpisodeProgress{
		PositionSec: positionSec,
		DurationSec: durationSec,
		UpdatedAt:   time.Now(),
	}
	src[animeKey] = anime
	pf[source] = src
	return WriteProgress(pf)
}

// GetEpisodeProgress, kayıtlı bölüm ilerlemesini döner; kayıt yoksa ok=false.
func GetEpisodeProgress(source, animeKey, episodeKey string) (EpisodeProgress, bool, error) {
	progressMu.Lock()
	defer progressMu.Unlock()

	pf, err := ReadProgress()
	if err != nil {
		return EpisodeProgress{}, false, err
	}
	anime, ok := pf[source][animeKey]
	if !ok {
		return EpisodeProgress{}, false, nil
	}
	prog, ok := anime[episodeKey]
	return prog, ok, nil
}

// ClearEpisodeProgress, bölüm bitince (veya kullanıcı baştan başlatınca) ilerlemeyi siler.
// Boş kalan anime/kaynak kayıtları da temizlenir. Kayıt zaten yoksa hata vermez.
func ClearEpisodeProgress(source, animeKey, episodeKey string) error {
	progressMu.Lock()
	defer progressMu.Unlock()

	pf, err := ReadProgress()
	if err != nil {
		return err
	}
	anime, ok := pf[source][animeKey]
	if !ok {
		return nil
	}
	delete(anime, episodeKey)
	if len(anime) == 0 {
		delete(pf[source], animeKey)
		if len(pf[source]) == 0 {
			delete(pf, source)
		}
	} else {
		pf[source][animeKey] = anime
	}
	return WriteProgress(pf)
}
