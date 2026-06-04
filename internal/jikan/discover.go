package jikan

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"strconv"
)

type ImageType struct {
	ImageURL      string `json:"image_url"`
	SmallImageURL string `json:"small_image_url"`
	LargeImageURL string `json:"large_image_url"`
}

type Images struct {
	Jpg  ImageType `json:"jpg"`
	WebP ImageType `json:"webp"`
}

type AnimeBasic struct {
	MalID        int     `json:"mal_id"`
	Title        string  `json:"title"`
	TitleEnglish string  `json:"title_english"`
	Titles       []struct {
		Type  string `json:"type"`
		Title string `json:"title"`
	} `json:"titles"`
	Score  float64 `json:"score"`
	Year   int     `json:"year"`
	Aired  struct {
		From string `json:"from"`
	} `json:"aired"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Images Images `json:"images"`
}

// GetThumbnail returns the small/regular image prioritizing webp format for list views
func (a *AnimeBasic) GetThumbnail() string {
	if a.Images.WebP.ImageURL != "" {
		return a.Images.WebP.ImageURL
	}
	return a.Images.Jpg.ImageURL
}

type AnimeListResponse struct {
	Data       []AnimeBasic `json:"data"`
	Pagination struct {
		HasNextPage bool `json:"has_next_page"`
	} `json:"pagination"`
}

type UserWatchlistResponse struct {
	Data []struct {
		Anime AnimeBasic `json:"anime"`
	} `json:"data"`
}

func fetchAnimeListMultiPage(apiURLBase string, maxPages int) ([]AnimeBasic, error) {
	var allAnimes []AnimeBasic
	client := &http.Client{Timeout: 10 * time.Second}

	for page := 1; page <= maxPages; page++ {
		apiURL := fmt.Sprintf("%s&page=%d", apiURLBase, page)
		resp, err := client.Get(apiURL)
		if err != nil {
			if page == 1 {
				return nil, err
			}
			break
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if page == 1 {
				return nil, fmt.Errorf("jikan API error: %s", resp.Status)
			}
			break
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			break
		}

		var listResp AnimeListResponse
		if err := json.Unmarshal(body, &listResp); err != nil {
			break
		}

		allAnimes = append(allAnimes, listResp.Data...)

		if !listResp.Pagination.HasNextPage {
			break
		}

		if page < maxPages {
			time.Sleep(400 * time.Millisecond)
		}
	}

	return allAnimes, nil
}

// GetTopAnime fetches the current top anime
func GetTopAnime() ([]AnimeBasic, error) {
	return fetchAnimeListMultiPage("https://api.jikan.moe/v4/top/anime?limit=25", 4) // 100 anime
}

// GetSeasonalAnime fetches the currently airing seasonal anime
func GetSeasonalAnime() ([]AnimeBasic, error) {
	return fetchAnimeListMultiPage("https://api.jikan.moe/v4/seasons/now?limit=25", 8) // max 200 anime
}

// GetUserWatchlist fetches the "Watching" list for a specific MAL user
type MALItem struct {
	AnimeTitle        string  `json:"anime_title"`
	AnimeID           int     `json:"anime_id"`
	AnimeScoreVal     float64 `json:"anime_score_val"`
	AnimeStartDateStr string  `json:"anime_start_date_string"`
	Genres            []struct {
		Name string `json:"name"`
	} `json:"genres"`
}

// GetUserWatchlist fetches the "Watching" list for a specific MAL user directly from MAL HTML
func GetUserWatchlist(username string) ([]AnimeBasic, error) {
	apiURL := fmt.Sprintf("https://myanimelist.net/animelist/%s?status=1", username)

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", apiURL, nil)
	// Add User-Agent to avoid basic blocks
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("MAL kullanıcısı bulunamadı: %s", username)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MyAnimeList'e ulaşılamadı (HTTP %d)", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(`data-items="([^"]+)"`)
	matches := re.FindSubmatch(body)
	if len(matches) < 2 {
		// It could mean the list is empty or private
		return []AnimeBasic{}, nil
	}

	rawJSON := html.UnescapeString(string(matches[1]))

	var items []MALItem
	if err := json.Unmarshal([]byte(rawJSON), &items); err != nil {
		return nil, err
	}

	var result []AnimeBasic
	for _, entry := range items {
		// Yılı ayrıştır (MM-DD-YY)
		year := 0
		parts := strings.Split(entry.AnimeStartDateStr, "-")
		if len(parts) == 3 {
			yy, err := strconv.Atoi(parts[2])
			if err == nil {
				if yy > 50 {
					year = 1900 + yy
				} else {
					year = 2000 + yy
				}
			}
		}

		// Genre dönüşümü
		var basicGenres []struct {
			Name string `json:"name"`
		}
		for _, g := range entry.Genres {
			basicGenres = append(basicGenres, struct {
				Name string `json:"name"`
			}{Name: g.Name})
		}

		result = append(result, AnimeBasic{
			MalID:  entry.AnimeID,
			Title:  entry.AnimeTitle,
			Score:  entry.AnimeScoreVal,
			Year:   year,
			Genres: basicGenres,
		})
	}

	return result, nil
}
// CleanTitle removes common tags and punctuation that prevent accurate matching.
func CleanTitle(title string) string {
	t := strings.ToLower(title)
	t = strings.ReplaceAll(t, " (tv)", "")
	t = strings.ReplaceAll(t, " (dub)", "")
	t = strings.ReplaceAll(t, " (sub)", "")
	
	// Remove all punctuation
	punctuation := []string{":", "-", "!", "?", ".", ",", ";", "'", "\""}
	for _, p := range punctuation {
		t = strings.ReplaceAll(t, p, " ")
	}

	// Collapse multiple spaces into one
	fields := strings.Fields(t)
	return strings.Join(fields, " ")
}

// LevenshteinDistance computes the Levenshtein distance between two strings.
func LevenshteinDistance(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)
	n, m := len(r1), len(r2)

	if n == 0 {
		return m
	}
	if m == 0 {
		return n
	}

	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			cost := 1
			if r1[i-1] == r2[j-1] {
				cost = 0
			}
			minCost := d[i-1][j] + 1
			if d[i][j-1]+1 < minCost {
				minCost = d[i][j-1] + 1
			}
			if d[i-1][j-1]+cost < minCost {
				minCost = d[i-1][j-1] + cost
			}
			d[i][j] = minCost
		}
	}
	return d[n][m]
}

// StringSimilarity returns a similarity score between 0.0 and 1.0.
func StringSimilarity(s1, s2 string) float64 {
	if s1 == "" || s2 == "" {
		if s1 == s2 {
			return 1.0
		}
		return 0.0
	}
	distance := LevenshteinDistance(s1, s2)
	maxLen := len([]rune(s1))
	if len([]rune(s2)) > maxLen {
		maxLen = len([]rune(s2))
	}
	return 1.0 - float64(distance)/float64(maxLen)
}

// MatchAnimeTitle tries to find the best match for a source title in Jikan API results.
func MatchAnimeTitle(sourceTitle string, jikanResults []AnimeBasic) *AnimeBasic {
	cleanSource := CleanTitle(sourceTitle)
	
	var bestMatch *AnimeBasic
	var bestScore float64 = 0.0

	for i := range jikanResults {
		j := &jikanResults[i]
		
		// 1. Exact matches always win
		if CleanTitle(j.Title) == cleanSource || CleanTitle(j.TitleEnglish) == cleanSource {
			return j
		}
		for _, alt := range j.Titles {
			if CleanTitle(alt.Title) == cleanSource {
				return j
			}
		}

		// Calculate similarities
		scores := []float64{
			StringSimilarity(cleanSource, CleanTitle(j.Title)),
			StringSimilarity(cleanSource, CleanTitle(j.TitleEnglish)),
		}
		for _, alt := range j.Titles {
			scores = append(scores, StringSimilarity(cleanSource, CleanTitle(alt.Title)))
		}

		// Check Contains as a high score alternative
		if cleanSource != "" {
			cleanJ := CleanTitle(j.Title)
			cleanEng := CleanTitle(j.TitleEnglish)
			if (cleanJ != "" && (strings.Contains(cleanSource, cleanJ) || strings.Contains(cleanJ, cleanSource))) || 
			   (cleanEng != "" && (strings.Contains(cleanSource, cleanEng) || strings.Contains(cleanEng, cleanSource))) {
				scores = append(scores, 0.85) // High base score for substring match
			}
		}

		for _, score := range scores {
			if score > bestScore {
				bestScore = score
				bestMatch = j
			}
		}
	}

	// 0.60 benzerlik barajı
	if bestScore >= 0.60 {
		return bestMatch
	}

	return nil
}
