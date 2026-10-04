package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultLatestReleaseURL = "https://api.github.com/repos/4444654/subconv-next/releases/latest"
const defaultLatestReleasePageURL = "https://github.com/4444654/subconv-next/releases/latest"

var latestReleaseURL = defaultLatestReleaseURL
var latestReleasePageURL = defaultLatestReleasePageURL
var updateCheckHTTPClient = &http.Client{Timeout: 8 * time.Second}

type updateCheckResponse struct {
	OK              bool   `json:"ok"`
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	Comparable      bool   `json:"comparable"`
	ReleaseURL      string `json:"release_url,omitempty"`
	ReleaseName     string `json:"release_name,omitempty"`
	PublishedAt     string `json:"published_at,omitempty"`
	CheckedAt       string `json:"checked_at"`
}

type githubLatestReleaseResponse struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	release, err := fetchLatestRelease(ctx)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "UPDATE_CHECK_FAILED", err.Error())
		return
	}

	current := strings.TrimSpace(s.version)
	latest := strings.TrimSpace(release.TagName)
	compare, comparable := compareVersionStrings(current, latest)

	writeJSON(w, http.StatusOK, updateCheckResponse{
		OK:              true,
		CurrentVersion:  current,
		LatestVersion:   latest,
		UpdateAvailable: comparable && compare < 0,
		Comparable:      comparable,
		ReleaseURL:      release.HTMLURL,
		ReleaseName:     release.Name,
		PublishedAt:     release.PublishedAt,
		CheckedAt:       time.Now().UTC().Format(time.RFC3339),
	})
}

func fetchLatestRelease(ctx context.Context) (githubLatestReleaseResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return githubLatestReleaseResponse{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "subconv-next-update-check")

	resp, err := doUpdateCheckRequest(req)
	if err != nil {
		return githubLatestReleaseResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		release, fallbackErr := fetchLatestReleaseFromRedirect(ctx)
		if fallbackErr == nil {
			return release, nil
		}
		return githubLatestReleaseResponse{}, fmt.Errorf("GitHub release API returned %s", resp.Status)
	}

	var release githubLatestReleaseResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return githubLatestReleaseResponse{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubLatestReleaseResponse{}, fmt.Errorf("latest release tag is empty")
	}
	release.HTMLURL = sanitizeReleaseURL(release.HTMLURL)
	return release, nil
}

func fetchLatestReleaseFromRedirect(ctx context.Context) (githubLatestReleaseResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleasePageURL, nil)
	if err != nil {
		return githubLatestReleaseResponse{}, err
	}
	req.Header.Set("User-Agent", "subconv-next-update-check")

	resp, err := doUpdateCheckRequest(req)
	if err != nil {
		return githubLatestReleaseResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubLatestReleaseResponse{}, fmt.Errorf("GitHub release page returned %s", resp.Status)
	}

	parts := strings.Split(strings.Trim(resp.Request.URL.Path, "/"), "/")
	if len(parts) < 3 || parts[len(parts)-2] != "tag" {
		return githubLatestReleaseResponse{}, fmt.Errorf("latest release redirect did not resolve to a tag")
	}
	tag, err := url.PathUnescape(parts[len(parts)-1])
	if err != nil {
		return githubLatestReleaseResponse{}, err
	}
	if strings.TrimSpace(tag) == "" {
		return githubLatestReleaseResponse{}, fmt.Errorf("latest release tag is empty")
	}

	return githubLatestReleaseResponse{
		TagName: tag,
		Name:    tag,
		HTMLURL: resp.Request.URL.String(),
	}, nil
}

func doUpdateCheckRequest(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("update check request URL is required")
	}
	baseClient := updateCheckHTTPClient
	if baseClient == nil {
		baseClient = http.DefaultClient
	}
	client := *baseClient
	existingRedirectPolicy := client.CheckRedirect
	initialURL := *req.URL
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("update check stopped after too many redirects")
		}
		if next.URL == nil || next.URL.User != nil ||
			!strings.EqualFold(next.URL.Scheme, initialURL.Scheme) ||
			!sameOriginAuthority(next.URL, &initialURL) {
			return fmt.Errorf("update check rejected a cross-origin redirect")
		}
		if existingRedirectPolicy != nil {
			return existingRedirectPolicy(next, via)
		}
		return nil
	}
	return client.Do(req)
}

func sanitizeReleaseURL(raw string) string {
	candidate, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || candidate.User != nil || candidate.Host == "" ||
		(!strings.EqualFold(candidate.Scheme, "http") && !strings.EqualFold(candidate.Scheme, "https")) {
		return ""
	}
	expected, err := url.Parse(strings.TrimSpace(latestReleasePageURL))
	if err != nil || expected.Host == "" || !strings.EqualFold(candidate.Scheme, expected.Scheme) || !sameOriginAuthority(candidate, expected) {
		return ""
	}
	return candidate.String()
}

var versionNumberPattern = regexp.MustCompile(`\d+`)

func compareVersionStrings(current, latest string) (int, bool) {
	currentParts := versionNumberPattern.FindAllString(strings.TrimSpace(current), -1)
	latestParts := versionNumberPattern.FindAllString(strings.TrimSpace(latest), -1)
	if len(currentParts) == 0 || len(latestParts) == 0 {
		return 0, false
	}

	maxLen := len(currentParts)
	if len(latestParts) > maxLen {
		maxLen = len(latestParts)
	}
	for i := 0; i < maxLen; i++ {
		currentValue := 0
		latestValue := 0
		if i < len(currentParts) {
			currentValue, _ = strconv.Atoi(currentParts[i])
		}
		if i < len(latestParts) {
			latestValue, _ = strconv.Atoi(latestParts[i])
		}
		if currentValue < latestValue {
			return -1, true
		}
		if currentValue > latestValue {
			return 1, true
		}
	}
	return 0, true
}
