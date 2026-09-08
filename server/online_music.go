package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/navidrome/navidrome/conf"
)

const onlineMusicAPIPath = "/api/online-music"

var onlineMusicClient = &http.Client{
	Timeout: 45 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}
var unsafeFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

type onlineSong struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Cover    string `json:"cover,omitempty"`
	Duration int64  `json:"duration,omitempty"`
}

type importOnlineSongRequest struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Album  string `json:"album"`
}

func (s *Server) MountOnlineMusicRouter() {
	router := chi.NewRouter()
	router.Get("/search", onlineMusicSearch)
	router.Get("/stream", onlineMusicStream)
	router.Get("/lyrics", onlineMusicLyrics)
	router.Post("/import", onlineMusicImport)
	s.router.Group(func(r chi.Router) {
		r.Use(Authenticator(s.ds))
		r.Use(JWTRefresher)
		r.Mount(filepath.ToSlash(filepath.Join(conf.Server.BasePath, onlineMusicAPIPath)), router)
	})
}

func onlineMusicSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "missing query"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 20
	}
	form := url.Values{"s": {query}, "type": {"1"}, "limit": {strconv.Itoa(limit)}, "offset": {"0"}}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://music.163.com/api/search/get/web", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": "online search failed"})
		return
	}
	defer resp.Body.Close()
	var payload struct {
		Result struct {
			Songs []struct {
				ID       json.Number `json:"id"`
				Name     string      `json:"name"`
				Duration int64       `json:"duration"`
				Artists  []struct{ Name string `json:"name"` } `json:"artists"`
				Album    struct {
					Name   string `json:"name"`
					PicURL string `json:"picUrl"`
				} `json:"album"`
			} `json:"songs"`
		} `json:"result"`
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if resp.StatusCode != http.StatusOK || decoder.Decode(&payload) != nil {
		writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid search response"})
		return
	}
	items := make([]onlineSong, 0, len(payload.Result.Songs))
	for _, song := range payload.Result.Songs {
		artists := make([]string, 0, len(song.Artists))
		for _, artist := range song.Artists { if artist.Name != "" { artists = append(artists, artist.Name) } }
		items = append(items, onlineSong{ID: song.ID.String(), Title: song.Name, Artist: strings.Join(artists, ", "), Album: song.Album.Name, Cover: song.Album.PicURL, Duration: song.Duration})
	}
	writeOnlineJSON(w, http.StatusOK, map[string]any{"items": items})
}

func resolveOnlineMusicURL(ctxReq *http.Request, id string) (string, error) {
	endpoint := "https://api.qijieya.cn/meting/?server=netease&type=url&id=" + url.QueryEscape(id) + "&br=320"
	req, _ := http.NewRequestWithContext(ctxReq.Context(), http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") { return location, nil }
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil { return "", err }
	value := strings.TrimSpace(string(body))
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") { return value, nil }
	var object struct{ URL string `json:"url"`; Data string `json:"data"` }
	if json.Unmarshal(body, &object) == nil {
		if object.URL != "" { return object.URL, nil }
		if object.Data != "" { return object.Data, nil }
	}
	var list []struct{ URL string `json:"url"` }
	if json.Unmarshal(body, &list) == nil && len(list) > 0 && list[0].URL != "" { return list[0].URL, nil }
	return "", fmt.Errorf("play URL unavailable")
}

func onlineMusicStream(w http.ResponseWriter, r *http.Request) {
	mediaURL, err := resolveOnlineMusicURL(r, r.URL.Query().Get("id"))
	if err != nil { http.Error(w, "stream unavailable", http.StatusBadGateway); return }
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, mediaURL, nil)
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" { req.Header.Set("Range", rangeHeader) }
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil { http.Error(w, "stream unavailable", http.StatusBadGateway); return }
	defer resp.Body.Close()
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} { if value := resp.Header.Get(header); value != "" { w.Header().Set(header, value) } }
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func fetchOnlineLyrics(r *http.Request, id string) (string, error) {
	endpoint := "https://music.163.com/api/song/lyric?id=" + url.QueryEscape(id) + "&lv=-1&kv=-1&tv=-1"
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()
	var payload struct{ LRC struct{ Lyric string `json:"lyric"` } `json:"lrc"` }
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&payload) != nil { return "", fmt.Errorf("lyrics unavailable") }
	return payload.LRC.Lyric, nil
}

func onlineMusicLyrics(w http.ResponseWriter, r *http.Request) {
	lyrics, err := fetchOnlineLyrics(r, r.URL.Query().Get("id"))
	if err != nil { writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()}); return }
	writeOnlineJSON(w, http.StatusOK, map[string]string{"lrc": lyrics})
}

func onlineMusicImport(w http.ResponseWriter, r *http.Request) {
	var song importOnlineSongRequest
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&song) != nil || song.ID == "" {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid song"}); return
	}
	mediaURL, err := resolveOnlineMusicURL(r, song.ID)
	if err != nil { writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()}); return }
	artist, album, title := safeOnlineName(song.Artist, "Unknown Artist"), safeOnlineName(song.Album, "Unknown Album"), safeOnlineName(song.Title, "Unknown Title")
	directory := filepath.Join(conf.Server.MusicFolder, artist, album)
	if err = os.MkdirAll(directory, 0755); err != nil { writeOnlineJSON(w, http.StatusInternalServerError, map[string]string{"error": "music folder is not writable"}); return }
	resp, err := onlineMusicClient.Get(mediaURL)
	if err != nil { writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": "download failed"}); return }
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 { writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": "download source rejected request"}); return }
	ext := filepath.Ext(strings.Split(mediaURL, "?")[0]); if ext == "" || len(ext) > 6 { ext = ".mp3" }
	base := filepath.Join(directory, title)
	temporary := base + ext + ".part"
	file, err := os.Create(temporary)
	if err == nil { _, err = io.Copy(file, resp.Body); closeErr := file.Close(); if err == nil { err = closeErr } }
	if err != nil { _ = os.Remove(temporary); writeOnlineJSON(w, http.StatusInternalServerError, map[string]string{"error": "saving audio failed"}); return }
	if err = os.Rename(temporary, base+ext); err != nil { writeOnlineJSON(w, http.StatusInternalServerError, map[string]string{"error": "saving audio failed"}); return }
	lyricsSaved := false
	if lyrics, lyricErr := fetchOnlineLyrics(r, song.ID); lyricErr == nil && strings.TrimSpace(lyrics) != "" { lyricsSaved = os.WriteFile(base+".lrc", []byte(lyrics), 0644) == nil }
	writeOnlineJSON(w, http.StatusOK, map[string]any{"ok": true, "file": filepath.Join(artist, album, title+ext), "lyricsSaved": lyricsSaved})
}

func safeOnlineName(value, fallback string) string {
	value = strings.TrimSpace(unsafeFilenameChars.ReplaceAllString(value, "_")); value = strings.TrimRight(value, ". ")
	if value == "" { return fallback }; return value
}

func writeOnlineJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8"); w.WriteHeader(status); _ = json.NewEncoder(w).Encode(value)
}
