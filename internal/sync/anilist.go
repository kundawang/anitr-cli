package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const anilistApiUrl = "https://graphql.anilist.co"

// GetAniListIdByMalId Jikan API'den gelen MAL ID'sini kullanarak AniList ID'sini ve max bölüm sayısını bulur.
func GetAniListIdByMalId(malId int) (int, int, error) {
	query := `
	query ($idMal: Int) {
		Media (idMal: $idMal, type: ANIME) {
			id
			episodes
		}
	}
	`
	variables := map[string]interface{}{
		"idMal": malId,
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"query":     query,
		"variables": variables,
	})

	resp, err := http.Post(anilistApiUrl, "application/json", bytes.NewBuffer(reqBody))
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("anilist api error: status code %d", resp.StatusCode)
	}

	var result struct {
		Data struct {
			Media struct {
				Id       int `json:"id"`
				Episodes int `json:"episodes"`
			} `json:"Media"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, 0, err
	}

	if result.Data.Media.Id == 0 {
		return 0, 0, fmt.Errorf("anime not found on anilist for mal id: %d", malId)
	}

	return result.Data.Media.Id, result.Data.Media.Episodes, nil
}

// ResolveAniListSequel AniList'in kendi veritabanındaki SEQUEL (devam) ilişkilerini kullanarak
// izlenen bölümün hangi sezonun hangi bölümüne denk geldiğini hesaplar.
// sourceSeasonNum: Kaynağın (Anizium vb.) bize söylediği sezon numarası. Eğer mevcut bölüm sınıra sığıyorsa
// ancak sourceSeasonNum henüz ulaşılan sezondan büyükse, Force-Skip ile zorla sonraki sezona atlanır.
func ResolveAniListSequel(anilistId int, progress int, sourceSeasonNum int) (int, int, int, error) {
	currentId := anilistId
	currentProgress := progress
	seasonsSkipped := 1 // Kök sezondan başlıyoruz (Jikan genelde kök sezonu bulur)

	for {
		query := `
		query ($id: Int) {
			Media (id: $id, type: ANIME) {
				id
				episodes
				relations {
					edges {
						relationType
						node {
							id
							episodes
							type
							format
						}
					}
				}
			}
		}
		`
		variables := map[string]interface{}{"id": currentId}
		reqBody, _ := json.Marshal(map[string]interface{}{"query": query, "variables": variables})

		resp, err := http.Post(anilistApiUrl, "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			return 0, 0, 0, err
		}

		var result struct {
			Data struct {
				Media struct {
					Id        int `json:"id"`
					Episodes  int `json:"episodes"`
					Relations struct {
						Edges []struct {
							RelationType string `json:"relationType"`
							Node         struct {
								Id       int    `json:"id"`
								Episodes int    `json:"episodes"`
								Type     string `json:"type"`
								Format   string `json:"format"`
							} `json:"node"`
						} `json:"edges"`
					} `json:"relations"`
				} `json:"Media"`
			} `json:"data"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return 0, 0, 0, err
		}
		resp.Body.Close()

		media := result.Data.Media
		if media.Id == 0 {
			return 0, 0, 0, fmt.Errorf("media not found")
		}

		// Force-Skip Heuristic:
		// Eğer animenin maksimum bölümü bilinmiyorsa (0) bu sezonda kal.
		// Eğer izlenen bölüm bu sezona SIKIYORSA (currentProgress <= media.Episodes),
		// ANCAK kaynağın belirttiği sezon (sourceSeasonNum) henüz ulaştığımız sezondan (seasonsSkipped) BÜYÜKSE,
		// izlediğimiz şey bu sezon DEĞİLDİR (Örn: S1 eksiktir, JJK S2 23. bölüm izleniyordur). Bu durumda ZORLA ATLA (Force-Skip).
		if media.Episodes == 0 {
			return currentId, currentProgress, 0, nil
		}

		forceSkip := false
		if currentProgress <= media.Episodes {
			if sourceSeasonNum > seasonsSkipped {
				forceSkip = true
			} else {
				return currentId, currentProgress, media.Episodes, nil
			}
		}

		// İzlenen bölüm maksimum bölümü aşıyor veya Force-Skip tetiklendi, SEQUEL (Devam) sezonunu bul
		var sequelId int
		for _, edge := range media.Relations.Edges {
			if edge.RelationType == "SEQUEL" && edge.Node.Type == "ANIME" {
				// Sadece TV veya ONA/OVA gibi ana formattaki animeleri dikkate al
				// Eğer birden fazla SEQUEL varsa, genellikle ilk dönen ana animedir
				if edge.Node.Format == "TV" || edge.Node.Format == "TV_SHORT" || edge.Node.Format == "ONA" || edge.Node.Format == "OVA" {
					sequelId = edge.Node.Id
					break
				}
			}
		}

		if sequelId == 0 {
			// Devam sezonu bulunamadıysa (ör. henüz yayınlanmadıysa)
			if forceSkip {
				// Zorla atlamaya çalıştık ama sezon yoksa olduğumuz yerde kalalım.
				return currentId, currentProgress, media.Episodes, nil
			}
			return currentId, media.Episodes, media.Episodes, nil
		}

		// Devam sezonu bulundu, ilerlemeyi ve ID'yi güncelle
		if !forceSkip {
			currentProgress -= media.Episodes
		}
		currentId = sequelId
		seasonsSkipped++
	}
}
// UpdateAniListProgress AniList üzerinde kullanıcının izleme durumunu ve bölüm sayısını günceller.
func UpdateAniListProgress(token string, anilistId int, progress int, status string) error {
	if token == "" {
		return fmt.Errorf("anilist token is empty")
	}

	mutation := `
	mutation ($mediaId: Int, $progress: Int, $status: MediaListStatus) {
		SaveMediaListEntry (mediaId: $mediaId, progress: $progress, status: $status) {
			id
			status
			progress
		}
	}
	`
	variables := map[string]interface{}{
		"mediaId":  anilistId,
		"progress": progress,
	}
	
	if status != "" {
		variables["status"] = status
	}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"query":     mutation,
		"variables": variables,
	})

	req, err := http.NewRequest("POST", anilistApiUrl, bytes.NewBuffer(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		var errResp map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errResp)
		return fmt.Errorf("anilist api error (mutation): status code %d, body: %v", resp.StatusCode, errResp)
	}

	return nil
}
