package server

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/navidrome/navidrome/conf"
	"github.com/navidrome/navidrome/model"
)

const onlineMusicAPIPath = "/api/online-music"

var onlineMusicClient = &http.Client{
	Timeout:       45 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
}
var unsafeFilenameChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
var playlistMatchSeparators = regexp.MustCompile(`[^\p{L}\p{N}]+`)
var onlineDownloadJobs = struct {
	sync.RWMutex
	items map[string]onlineDownloadJob
}{items: map[string]onlineDownloadJob{}}

type onlineDownloadJob struct {
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	File        string `json:"file,omitempty"`
	LyricsSaved bool   `json:"lyricsSaved,omitempty"`
	Error       string `json:"error,omitempty"`
}

type onlineSong struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Cover    string `json:"cover,omitempty"`
	Duration int64  `json:"duration,omitempty"`
	Provider string `json:"provider"`
	Local    bool   `json:"local,omitempty"`
}

type importOnlineSongRequest struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	Provider string `json:"provider"`
}

type playlistImportTrack struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Path   string `json:"path,omitempty"`
}

type playlistImportRequest struct {
	Name   string                `json:"name"`
	Tracks []playlistImportTrack `json:"tracks"`
}

func (s *Server) MountOnlineMusicRouter() {
	router := chi.NewRouter()
	router.Get("/search", s.onlineMusicSearch)
	router.Get("/stream", onlineMusicStream)
	router.Get("/lyrics", onlineMusicLyrics)
	router.Post("/import", onlineMusicImport)
	router.Post("/import/start", onlineMusicImportStart)
	router.Get("/import/status/{id}", onlineMusicImportStatus)
	router.Post("/auto-lyrics", s.onlineAutoLyrics)
	router.Post("/playlist-match", s.onlinePlaylistMatch)
	router.Post("/playlist-import", onlinePlaylistImport)
	s.router.Group(func(r chi.Router) {
		r.Use(Authenticator(s.ds))
		r.Use(JWTRefresher)
		r.Mount(filepath.ToSlash(filepath.Join(conf.Server.BasePath, onlineMusicAPIPath)), router)
	})
}

func (s *Server) onlinePlaylistMatch(w http.ResponseWriter, r *http.Request) {
	var payload playlistImportRequest
	if json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&payload) != nil || len(payload.Tracks) == 0 {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid playlist"})
		return
	}
	if len(payload.Tracks) > 1000 {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "playlist is too large"})
		return
	}
	result := make([]map[string]any, 0, len(payload.Tracks))
	for _, requested := range payload.Tracks {
		query := strings.TrimSpace(requested.Title + " " + requested.Artist)
		matches, err := s.ds.MediaFile(r.Context()).Search(query, model.QueryOptions{Max: 8})
		var best *model.MediaFile
		bestScore := -1
		if err == nil {
			for i := range matches {
				score := playlistMatchScore(requested, matches[i])
				if score > bestScore {
					best, bestScore = &matches[i], score
				}
			}
		}
		item := map[string]any{"title": requested.Title, "artist": requested.Artist, "matched": false}
		if best != nil && bestScore >= 60 {
			item["matched"] = true
			item["path"] = best.Path
			item["localTitle"] = best.Title
			item["localArtist"] = best.Artist
		}
		result = append(result, item)
	}
	writeOnlineJSON(w, http.StatusOK, map[string]any{"name": payload.Name, "tracks": result})
}

func playlistMatchScore(requested playlistImportTrack, candidate model.MediaFile) int {
	norm := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		return strings.Join(strings.Fields(playlistMatchSeparators.ReplaceAllString(value, " ")), " ")
	}
	title, candidateTitle := norm(requested.Title), norm(candidate.Title)
	artist, candidateArtist := norm(requested.Artist), norm(candidate.Artist)
	score := 0
	if title != "" && title == candidateTitle {
		score += 70
	} else if title != "" && (strings.Contains(candidateTitle, title) || strings.Contains(title, candidateTitle)) {
		score += 45
	}
	if artist != "" && artist == candidateArtist {
		score += 30
	} else if artist != "" && (strings.Contains(candidateArtist, artist) || strings.Contains(artist, candidateArtist)) {
		score += 15
	}
	return score
}

func onlinePlaylistImport(w http.ResponseWriter, r *http.Request) {
	var payload playlistImportRequest
	if json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&payload) != nil || len(payload.Tracks) == 0 {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid playlist"})
		return
	}
	name := safeOnlineName(payload.Name, "Imported Playlist")
	directory := filepath.Join(conf.Server.MusicFolder, "Imported Playlists")
	if err := os.MkdirAll(directory, 0755); err != nil {
		writeOnlineJSON(w, http.StatusInternalServerError, map[string]string{"error": "music folder is not writable"})
		return
	}
	var content strings.Builder
	content.WriteString("#EXTM3U\n")
	count := 0
	for _, track := range payload.Tracks {
		trackPath := filepath.Clean(strings.TrimSpace(track.Path))
		if trackPath == "." || filepath.IsAbs(trackPath) || strings.HasPrefix(trackPath, "..") {
			continue
		}
		content.WriteString(filepath.ToSlash(filepath.Join("..", trackPath)) + "\n")
		count++
	}
	if count == 0 {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "playlist has no matched tracks"})
		return
	}
	filename := filepath.Join(directory, name+".m3u8")
	if err := os.WriteFile(filename, []byte(content.String()), 0644); err != nil {
		writeOnlineJSON(w, http.StatusInternalServerError, map[string]string{"error": "saving playlist failed"})
		return
	}
	writeOnlineJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "tracks": count, "file": filepath.ToSlash(filepath.Join("Imported Playlists", name+".m3u8"))})
}

func (s *Server) onlineMusicSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "missing query"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 50 {
		limit = 20
	}
	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	if provider == "" {
		provider = "all"
	}
	providers := []string{provider}
	if provider == "all" {
		providers = []string{"netease", "qq", "kugou"}
	}
	type result struct{ items []onlineSong }
	results := make(chan result, len(providers))
	var wait sync.WaitGroup
	for _, source := range providers {
		wait.Add(1)
		go func(source string) {
			defer wait.Done()
			items, _ := searchOnlineProvider(r, source, query, limit)
			results <- result{items: items}
		}(source)
	}
	wait.Wait()
	close(results)
	items := make([]onlineSong, 0, limit*(len(providers)+1))
	seen := map[string]bool{}
	localItems, localErr := s.ds.MediaFile(r.Context()).Search(query, model.QueryOptions{Max: limit})
	if localErr == nil {
		for _, song := range localItems {
			item := onlineSong{ID: song.ID, Title: song.Title, Artist: song.Artist, Album: song.Album, Duration: int64(song.Duration * 1000), Provider: "local", Local: true}
			items = append(items, item)
			seen[onlineSongIdentity(item)] = true
		}
	}
	for result := range results {
		for _, item := range result.items {
			key := onlineSongIdentity(item)
			if !seen[key] {
				items = append(items, item)
				seen[key] = true
			}
		}
	}
	writeOnlineJSON(w, http.StatusOK, map[string]any{"items": items, "localCount": len(localItems)})
}

func onlineSongIdentity(item onlineSong) string {
	normalize := func(value string) string {
		return strings.Join(strings.Fields(playlistMatchSeparators.ReplaceAllString(strings.ToLower(value), " ")), " ")
	}
	return normalize(item.Title) + "|" + normalize(item.Artist)
}

func searchOnlineProvider(r *http.Request, provider, query string, limit int) ([]onlineSong, error) {
	switch provider {
	case "qq":
		return searchQQ(r, query, limit)
	case "kugou":
		return searchKugou(r, query, limit)
	default:
		return searchNetease(r, query, limit)
	}
}

func searchNetease(r *http.Request, query string, limit int) ([]onlineSong, error) {
	requestPayload := map[string]any{
		"s": query, "type": 1, "limit": limit, "offset": 0, "total": true,
		"header": map[string]any{"os": "pc", "appver": "3.1.19.204510", "requestId": "0", "deviceId": fmt.Sprintf("%x", md5.Sum([]byte(query+time.Now().String()))), "MUSIC_U": ""},
		"e_r":    true,
	}
	body, err := neteaseEAPIRequest(r, "/api/cloudsearch/pc", requestPayload)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Result struct {
			Songs []struct {
				ID       json.Number `json:"id"`
				Name     string      `json:"name"`
				Duration int64       `json:"duration"`
				DT       int64       `json:"dt"`
				Artists  []struct {
					Name string `json:"name"`
				} `json:"artists"`
				AR []struct {
					Name string `json:"name"`
				} `json:"ar"`
				Album struct {
					Name   string `json:"name"`
					PicURL string `json:"picUrl"`
				} `json:"album"`
				AL struct {
					Name   string `json:"name"`
					PicURL string `json:"picUrl"`
				} `json:"al"`
			} `json:"songs"`
		} `json:"result"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if decoder.Decode(&payload) != nil {
		return nil, fmt.Errorf("invalid search response")
	}
	items := make([]onlineSong, 0, len(payload.Result.Songs))
	for _, song := range payload.Result.Songs {
		artists := make([]string, 0, len(song.Artists)+len(song.AR))
		for _, artist := range append(song.Artists, song.AR...) {
			if artist.Name != "" {
				artists = append(artists, artist.Name)
			}
		}
		album, cover := song.Album.Name, song.Album.PicURL
		if album == "" {
			album = song.AL.Name
		}
		if cover == "" {
			cover = song.AL.PicURL
		}
		duration := song.Duration
		if duration == 0 {
			duration = song.DT
		}
		items = append(items, onlineSong{ID: song.ID.String(), Title: song.Name, Artist: strings.Join(artists, ", "), Album: album, Cover: cover, Duration: duration, Provider: "netease"})
	}
	return items, nil
}

func searchQQ(r *http.Request, query string, limit int) ([]onlineSong, error) {
	payload := map[string]any{"comm": map[string]string{"ct": "19", "cv": "1859", "uin": "0"}, "req_1": map[string]any{"method": "DoSearchForQQMusicDesktop", "module": "music.search.SearchCgiService", "param": map[string]any{"grp": 1, "num_per_page": limit, "page_num": 1, "query": query, "search_type": 0}}}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://u6.y.qq.com/cgi-bin/musicu.fcg", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		Req struct {
			Data struct {
				Body struct {
					Song struct {
						List []struct {
							Mid, Name string
							Singer    []struct{ Name string }
							Album     struct{ Name, Mid string }
							Interval  int64
						} `json:"list"`
					} `json:"song"`
				} `json:"body"`
			} `json:"data"`
		} `json:"req_1"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return nil, fmt.Errorf("invalid QQ response")
	}
	items := []onlineSong{}
	for _, song := range data.Req.Data.Body.Song.List {
		artists := []string{}
		for _, a := range song.Singer {
			artists = append(artists, a.Name)
		}
		cover := ""
		if song.Album.Mid != "" {
			cover = "https://y.gtimg.cn/music/photo_new/T002R300x300M000" + song.Album.Mid + ".jpg"
		}
		items = append(items, onlineSong{ID: song.Mid, Title: song.Name, Artist: strings.Join(artists, ", "), Album: song.Album.Name, Cover: cover, Duration: song.Interval * 1000, Provider: "qq"})
	}
	return items, nil
}

func searchKugou(r *http.Request, query string, limit int) ([]onlineSong, error) {
	endpoint := "https://songsearch.kugou.com/song_search_v2?format=json&platform=WebFilter&page=1&pagesize=" + strconv.Itoa(limit) + "&keyword=" + url.QueryEscape(query)
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		Data struct {
			Lists []struct {
				FileHash, SongName, SingerName, AlbumName string
				Duration                                  int64
				Trans                                     struct {
					Cover string `json:"union_cover"`
				} `json:"trans_param"`
			} `json:"lists"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return nil, fmt.Errorf("invalid Kugou response")
	}
	items := []onlineSong{}
	for _, song := range data.Data.Lists {
		cover := strings.ReplaceAll(song.Trans.Cover, "{size}", "400")
		items = append(items, onlineSong{ID: song.FileHash, Title: song.SongName, Artist: song.SingerName, Album: song.AlbumName, Cover: cover, Duration: song.Duration * 1000, Provider: "kugou"})
	}
	return items, nil
}

func neteaseEAPIRequest(r *http.Request, uri string, payload any) ([]byte, error) {
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	digest := md5.Sum([]byte("nobody" + uri + "use" + string(plain) + "md5forencrypt"))
	message := uri + "-36cd479b6b5-" + string(plain) + "-36cd479b6b5-" + hex.EncodeToString(digest[:])
	key := []byte("e82ckenh8dichen8")
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padding := block.BlockSize() - len(message)%block.BlockSize()
	padded := append([]byte(message), bytes.Repeat([]byte{byte(padding)}, padding)...)
	encrypted := make([]byte, len(padded))
	for start := 0; start < len(padded); start += block.BlockSize() {
		block.Encrypt(encrypted[start:start+block.BlockSize()], padded[start:start+block.BlockSize()])
	}
	form := url.Values{"params": {strings.ToUpper(hex.EncodeToString(encrypted))}}
	endpoint := "https://interface.music.163.com/eapi" + strings.TrimPrefix(uri, "/api")
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) NeteasyMusicDesktop/3.1.19.204510")
	req.Header.Set("Cookie", "os=pc; appver=3.1.19.204510")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("netease status %d", resp.StatusCode)
	}
	if json.Valid(content) {
		return content, nil
	}
	if len(content)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("invalid encrypted response")
	}
	decrypted := make([]byte, len(content))
	for start := 0; start < len(content); start += block.BlockSize() {
		block.Decrypt(decrypted[start:start+block.BlockSize()], content[start:start+block.BlockSize()])
	}
	if len(decrypted) > 0 {
		pad := int(decrypted[len(decrypted)-1])
		if pad > 0 && pad <= block.BlockSize() && pad <= len(decrypted) {
			decrypted = decrypted[:len(decrypted)-pad]
		}
	}
	return decrypted, nil
}

func resolveOnlineMusicURL(ctxReq *http.Request, provider, id string) (string, error) {
	switch provider {
	case "qq":
		for quality := 10; quality >= 0; quality-- {
			endpoint := "https://api.vkeys.cn/v2/music/tencent/geturl?mid=" + url.QueryEscape(id) + "&quality=" + strconv.Itoa(quality)
			req, _ := http.NewRequestWithContext(ctxReq.Context(), http.MethodGet, endpoint, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0")
			resp, err := onlineMusicClient.Do(req)
			if err != nil {
				continue
			}
			var data struct {
				Code int
				Data struct {
					URL string `json:"url"`
				} `json:"data"`
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(&data)
			resp.Body.Close()
			if decodeErr == nil && strings.HasPrefix(data.Data.URL, "http") {
				return data.Data.URL, nil
			}
		}
		return "", fmt.Errorf("QQ play URL unavailable")
	case "kugou":
		for _, base := range []string{"https://musicapi.haitangw.net/kgqq/kg.php", "https://music.haitangw.cc/kgqq/kg.php"} {
			for _, level := range []string{"hires", "lossless", "exhigh"} {
				endpoint := base + "?type=json&id=" + url.QueryEscape(id) + "&level=" + level
				req, _ := http.NewRequestWithContext(ctxReq.Context(), http.MethodGet, endpoint, nil)
				resp, err := onlineMusicClient.Do(req)
				if err != nil {
					continue
				}
				var data struct {
					Data struct {
						URL string `json:"url"`
					} `json:"data"`
				}
				decodeErr := json.NewDecoder(resp.Body).Decode(&data)
				resp.Body.Close()
				if decodeErr == nil && strings.HasPrefix(data.Data.URL, "http") {
					return data.Data.URL, nil
				}
			}
		}
		return "", fmt.Errorf("Kugou play URL unavailable")
	}
	endpoint := "https://api.qijieya.cn/meting/?server=netease&type=url&id=" + url.QueryEscape(id) + "&br=320"
	req, _ := http.NewRequestWithContext(ctxReq.Context(), http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		location := resp.Header.Get("Location")
		if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
			return location, nil
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(body))
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		return value, nil
	}
	var object struct {
		URL  string `json:"url"`
		Data string `json:"data"`
	}
	if json.Unmarshal(body, &object) == nil {
		if object.URL != "" {
			return object.URL, nil
		}
		if object.Data != "" {
			return object.Data, nil
		}
	}
	var list []struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(body, &list) == nil && len(list) > 0 && list[0].URL != "" {
		return list[0].URL, nil
	}
	return "", fmt.Errorf("play URL unavailable")
}

func onlineMusicStream(w http.ResponseWriter, r *http.Request) {
	mediaURL, err := resolveOnlineMusicURL(r, r.URL.Query().Get("provider"), r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusBadGateway)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, mediaURL, nil)
	if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		http.Error(w, "stream unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, header := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func fetchOnlineLyrics(r *http.Request, provider, id string) (string, error) {
	if provider == "qq" {
		endpoint := "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?songmid=" + url.QueryEscape(id) + "&g_tk=5381&loginUin=0&hostUin=0&format=json&inCharset=utf8&outCharset=utf-8&platform=yqq"
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
		req.Header.Set("Referer", "https://y.qq.com/portal/player.html")
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := onlineMusicClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var data struct {
			Lyric string `json:"lyric"`
		}
		if json.NewDecoder(resp.Body).Decode(&data) != nil {
			return "", fmt.Errorf("QQ lyrics unavailable")
		}
		decoded, err := base64.StdEncoding.DecodeString(data.Lyric)
		return string(decoded), err
	}
	if provider == "kugou" {
		searchURL := "http://lyrics.kugou.com/search?duration=-1&hash=" + url.QueryEscape(id)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
		resp, err := onlineMusicClient.Do(req)
		if err != nil {
			return "", err
		}
		var search struct {
			Candidates []struct{ ID, AccessKey string } `json:"candidates"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&search)
		resp.Body.Close()
		if decodeErr != nil || len(search.Candidates) == 0 {
			return "", fmt.Errorf("Kugou lyrics unavailable")
		}
		candidate := search.Candidates[0]
		downloadURL := "http://lyrics.kugou.com/download?ver=1&client=pc&fmt=lrc&charset=utf8&id=" + url.QueryEscape(candidate.ID) + "&accesskey=" + url.QueryEscape(candidate.AccessKey)
		req, _ = http.NewRequestWithContext(r.Context(), http.MethodGet, downloadURL, nil)
		resp, err = onlineMusicClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		var lyric struct {
			Content string `json:"content"`
		}
		if json.NewDecoder(resp.Body).Decode(&lyric) != nil {
			return "", fmt.Errorf("Kugou lyrics unavailable")
		}
		decoded, err := base64.StdEncoding.DecodeString(lyric.Content)
		return string(decoded), err
	}
	endpoint := "https://music.163.com/api/song/lyric?id=" + url.QueryEscape(id) + "&lv=-1&kv=-1&tv=-1"
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload struct {
		LRC struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&payload) != nil {
		return "", fmt.Errorf("lyrics unavailable")
	}
	return payload.LRC.Lyric, nil
}

func onlineMusicLyrics(w http.ResponseWriter, r *http.Request) {
	lyrics, err := fetchOnlineLyrics(r, r.URL.Query().Get("provider"), r.URL.Query().Get("id"))
	if err != nil {
		writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeOnlineJSON(w, http.StatusOK, map[string]string{"lrc": lyrics})
}

func onlineMusicImport(w http.ResponseWriter, r *http.Request) {
	var song importOnlineSongRequest
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&song) != nil || song.ID == "" {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid song"})
		return
	}
	result, err := downloadOnlineSong(r, song, nil)
	if err != nil {
		writeOnlineJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeOnlineJSON(w, http.StatusOK, result)
}

func onlineMusicImportStart(w http.ResponseWriter, r *http.Request) {
	var song importOnlineSongRequest
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&song) != nil || song.ID == "" || song.Provider == "local" {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid song"})
		return
	}
	random := make([]byte, 12)
	_, _ = rand.Read(random)
	jobID := hex.EncodeToString(random)
	setOnlineDownloadJob(jobID, onlineDownloadJob{Status: "preparing", Progress: 2})
	backgroundRequest := r.Clone(context.Background())
	go func() {
		result, err := downloadOnlineSong(backgroundRequest, song, func(progress int) {
			setOnlineDownloadJob(jobID, onlineDownloadJob{Status: "downloading", Progress: progress})
		})
		if err != nil {
			setOnlineDownloadJob(jobID, onlineDownloadJob{Status: "failed", Error: err.Error()})
			return
		}
		setOnlineDownloadJob(jobID, onlineDownloadJob{Status: "completed", Progress: 100, File: result["file"].(string), LyricsSaved: result["lyricsSaved"].(bool)})
	}()
	writeOnlineJSON(w, http.StatusAccepted, map[string]string{"jobId": jobID})
}

func (s *Server) onlineAutoLyrics(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		SongID string `json:"songId"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&payload) != nil || payload.SongID == "" {
		writeOnlineJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid song"})
		return
	}
	mediaFile, err := s.ds.MediaFile(r.Context()).Get(payload.SongID)
	if err != nil {
		writeOnlineJSON(w, http.StatusNotFound, map[string]string{"error": "song not found"})
		return
	}
	if strings.TrimSpace(mediaFile.Title) == "" || strings.TrimSpace(mediaFile.Artist) == "" || strings.Contains(strings.ToLower(mediaFile.Artist), "unknown") {
		writeOnlineJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "歌曲缺少完整的歌名或歌手，请先编辑音乐标签后再搜索歌词"})
		return
	}
	requested := playlistImportTrack{Title: mediaFile.Title, Artist: mediaFile.Artist}
	var best *onlineSong
	bestScore := -1
	for _, provider := range []string{"netease", "qq", "kugou"} {
		items, searchErr := searchOnlineProvider(r, provider, mediaFile.Title+" "+mediaFile.Artist, 5)
		if searchErr != nil {
			continue
		}
		for i := range items {
			score := playlistMatchScore(requested, model.MediaFile{Title: items[i].Title, Artist: items[i].Artist})
			if score > bestScore {
				best, bestScore = &items[i], score
			}
		}
	}
	if best == nil || bestScore < 70 {
		writeOnlineJSON(w, http.StatusNotFound, map[string]string{"error": "网上没有找到可信的匹配歌词"})
		return
	}
	lyrics, err := fetchOnlineLyrics(r, best.Provider, best.ID)
	if err != nil || strings.TrimSpace(lyrics) == "" {
		writeOnlineJSON(w, http.StatusNotFound, map[string]string{"error": "匹配到歌曲，但该来源没有歌词"})
		return
	}
	base := strings.TrimSuffix(mediaFile.AbsolutePath(), filepath.Ext(mediaFile.AbsolutePath()))
	if err = os.WriteFile(base+".lrc", []byte(lyrics), 0644); err != nil {
		writeOnlineJSON(w, http.StatusInternalServerError, map[string]string{"error": "找到歌词，但音乐目录不可写"})
		return
	}
	writeOnlineJSON(w, http.StatusOK, map[string]any{"ok": true, "lrc": lyrics, "provider": best.Provider})
}

func onlineMusicImportStatus(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "id")
	onlineDownloadJobs.RLock()
	job, ok := onlineDownloadJobs.items[jobID]
	onlineDownloadJobs.RUnlock()
	if !ok {
		writeOnlineJSON(w, http.StatusNotFound, map[string]string{"error": "download job not found"})
		return
	}
	writeOnlineJSON(w, http.StatusOK, job)
}

func setOnlineDownloadJob(jobID string, job onlineDownloadJob) {
	onlineDownloadJobs.Lock()
	onlineDownloadJobs.items[jobID] = job
	onlineDownloadJobs.Unlock()
}

func downloadOnlineSong(r *http.Request, song importOnlineSongRequest, reportProgress func(int)) (map[string]any, error) {
	mediaURL, err := resolveOnlineMusicURL(r, song.Provider, song.ID)
	if err != nil {
		return nil, err
	}
	artist, album, title := safeOnlineName(song.Artist, "Unknown Artist"), safeOnlineName(song.Album, "Unknown Album"), safeOnlineName(song.Title, "Unknown Title")
	directory := filepath.Join(conf.Server.MusicFolder, artist, album)
	if err = os.MkdirAll(directory, 0755); err != nil {
		return nil, fmt.Errorf("music folder is not writable")
	}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, mediaURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := onlineMusicClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download source rejected request")
	}
	ext := onlineAudioExtension(mediaURL, resp.Header.Get("Content-Type"))
	base := filepath.Join(directory, title)
	temporary := base + ext + ".part"
	file, err := os.Create(temporary)
	if err == nil {
		var written int64
		buffer := make([]byte, 128*1024)
		for err == nil {
			var read int
			read, err = resp.Body.Read(buffer)
			if read > 0 {
				var saved int
				saved, err = file.Write(buffer[:read])
				written += int64(saved)
				if reportProgress != nil && resp.ContentLength > 0 {
					progress := 5 + int(float64(written)/float64(resp.ContentLength)*85)
					if progress > 90 {
						progress = 90
					}
					reportProgress(progress)
				}
			}
			if err == io.EOF {
				err = nil
				break
			}
		}
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		_ = os.Remove(temporary)
		return nil, fmt.Errorf("saving audio failed")
	}
	if err = os.Rename(temporary, base+ext); err != nil {
		return nil, fmt.Errorf("saving audio failed")
	}
	if reportProgress != nil {
		reportProgress(94)
	}
	lyricsSaved := false
	if lyrics, lyricErr := fetchOnlineLyrics(r, song.Provider, song.ID); lyricErr == nil && strings.TrimSpace(lyrics) != "" {
		lyricsSaved = os.WriteFile(base+".lrc", []byte(lyrics), 0644) == nil
	}
	return map[string]any{"ok": true, "file": filepath.Join(artist, album, title+ext), "lyricsSaved": lyricsSaved}, nil
}

func onlineAudioExtension(mediaURL, contentType string) string {
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		if ext := map[string]string{
			"audio/aac":    ".aac",
			"audio/aacp":   ".aac",
			"audio/mp4":    ".m4a",
			"audio/x-m4a":  ".m4a",
			"audio/mpeg":   ".mp3",
			"audio/flac":   ".flac",
			"audio/x-flac": ".flac",
			"audio/ogg":    ".ogg",
			"audio/opus":   ".opus",
			"audio/wav":    ".wav",
			"audio/x-wav":  ".wav",
		}[strings.ToLower(parsed)]; ext != "" {
			return ext
		}
	}
	ext := strings.ToLower(filepath.Ext(strings.Split(mediaURL, "?")[0]))
	for _, supported := range []string{".aac", ".m4a", ".mp3", ".flac", ".ogg", ".opus", ".wav"} {
		if ext == supported {
			return ext
		}
	}
	return ".mp3"
}

func safeOnlineName(value, fallback string) string {
	value = strings.TrimSpace(unsafeFilenameChars.ReplaceAllString(value, "_"))
	value = strings.TrimRight(value, ". ")
	if value == "" {
		return fallback
	}
	return value
}

func writeOnlineJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
