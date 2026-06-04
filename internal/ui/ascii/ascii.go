package ascii

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"time"
)

// resize performs a simple nearest-neighbor scaling
func resize(img image.Image, targetW, targetH int) *image.RGBA {
	bounds := img.Bounds()
	w, h := bounds.Max.X, bounds.Max.Y
	res := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for y := 0; y < targetH; y++ {
		for x := 0; x < targetW; x++ {
			srcX := x * w / targetW
			srcY := y * h / targetH
			res.Set(x, y, img.At(srcX, srcY))
		}
	}
	return res
}

// FetchImage downloads the given image URL and returns the decoded image.
func FetchImage(url string) (image.Image, error) {
	if strings.Contains(url, ".webp") {
		url = strings.ReplaceAll(url, ".webp", ".jpg")
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("resim indirilemedi: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("resim indirilemedi. HTTP: %d", resp.StatusCode)
	}

	img, _, err := image.Decode(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("resim decode edilemedi: %w", err)
	}
	return img, nil
}

// GenerateASCIIArtFromImage converts an already decoded image to an ASCII string using solid Half-Blocks.
func GenerateASCIIArtFromImage(img image.Image, targetW int) string {
	bounds := img.Bounds()
	w, h := bounds.Max.X, bounds.Max.Y
	
	// Terminal proportions: 1 character is roughly half as wide as it is tall.
	targetH := int(float64(h) / float64(w) * float64(targetW)) 
	if targetH%2 != 0 {
		targetH++
	}

	resized := resize(img, targetW, targetH)

	var sb strings.Builder
	for y := 0; y < targetH; y += 2 {
		for x := 0; x < targetW; x++ {
			topColor := resized.RGBAAt(x, y)
			bottomColor := resized.RGBAAt(x, y+1)
			
			// ▀ (U+2580) upper half block: Foreground is top pixel, Background is bottom pixel.
			sb.WriteString(fmt.Sprintf("\033[38;2;%d;%d;%dm\033[48;2;%d;%d;%dm▀\033[0m", 
				topColor.R, topColor.G, topColor.B, 
				bottomColor.R, bottomColor.G, bottomColor.B))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// GenerateASCIIArt downloads the given image URL and returns it as an ASCII string.
func GenerateASCIIArt(url string, targetW int) string {
	img, err := FetchImage(url)
	if err != nil {
		return fmt.Sprintf("\033[31m[!] %v\033[0m\n", err)
	}
	return GenerateASCIIArtFromImage(img, targetW)
}
