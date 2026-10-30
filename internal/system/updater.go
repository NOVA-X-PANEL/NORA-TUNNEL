package system

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	GitHubRepo = "NOVA-X-PANEL/NORA-TUNNEL"
)

type ReleaseInfo struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
}

func FetchLatestRelease() (*ReleaseInfo, error) {
	client := &http.Client{Timeout: 8 * time.Second}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", GitHubRepo)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NORA-TUNNEL-Updater/1.0")
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to check latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}

	return &rel, nil
}

func PerformUpdate() (string, error) {
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("unsupported system architecture: %s", arch)
	}

	rel, err := FetchLatestRelease()
	if err != nil {
		return "", err
	}

	tag := rel.TagName
	if tag == "" {
		tag = "latest"
	}

	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/nora-tunnel-linux-%s.tar.gz", GitHubRepo, tag, arch)
	if tag == "latest" {
		downloadURL = fmt.Sprintf("https://github.com/%s/releases/latest/download/nora-tunnel-linux-%s.tar.gz", GitHubRepo, arch)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "NORA-TUNNEL-Updater/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download update package: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download server returned HTTP %s", resp.Status)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("invalid gzip archive: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	tempBinPath := "/tmp/nora-tunnel-update-bin"
	found := false

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		if header.Name == "nora-tunnel" || strings.HasSuffix(header.Name, "/nora-tunnel") {
			f, err := os.OpenFile(tempBinPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0755)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(f, tarReader); err != nil {
				f.Close()
				return "", err
			}
			f.Close()
			found = true
			break
		}
	}

	if !found {
		return "", errors.New("nora-tunnel binary not found inside downloaded archive")
	}

	// Identify target binary path
	targetBin := "/usr/local/bin/nora-tunnel"
	exePath, err := os.Executable()
	if err == nil && exePath != "" && !strings.Contains(exePath, "/tmp/") {
		targetBin = exePath
	}

	// Replace executable safely
	_ = os.Chmod(tempBinPath, 0755)
	if err := exec.Command("mv", "-f", tempBinPath, targetBin).Run(); err != nil {
		// Fallback to cp
		if cpErr := exec.Command("cp", "-f", tempBinPath, targetBin).Run(); cpErr != nil {
			return "", fmt.Errorf("failed to replace binary at %s: %w", targetBin, err)
		}
	}
	_ = os.Chmod(targetBin, 0755)

	// Also update CLI helper script /usr/local/bin/nora if possible
	go func() {
		noraScriptURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/main/nora.sh", GitHubRepo)
		if noraResp, err := http.Get(noraScriptURL); err == nil && noraResp.StatusCode == http.StatusOK {
			defer noraResp.Body.Close()
			if data, err := io.ReadAll(noraResp.Body); err == nil {
				_ = os.WriteFile("/usr/local/bin/nora", data, 0755)
			}
		}
	}()

	// Trigger restart in background
	go func() {
		time.Sleep(1200 * time.Millisecond)
		_ = exec.Command("systemctl", "restart", "nora-tunnel").Run()
	}()

	return tag, nil
}
