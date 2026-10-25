package catalogue

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var ErrChecksumNotFound = errors.New("no checksum found for file")

var ChecksumFileCandidates = []string{"SHA256SUMS", "sha256sum.txt", "SHA256SUMS.txt", "SHA256SUM", "sha256sums.txt"}

var bsdChecksumLine = regexp.MustCompile(`^SHA256\s*\((.+)\)\s*=\s*([0-9A-Fa-f]{64})$`)

func NormaliseSHA256(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 64 {
		return "", fmt.Errorf("a SHA-256 checksum is 64 hexadecimal characters, got %d", len(s))
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", errors.New("checksum is not hexadecimal")
	}
	return s, nil
}

func ParseChecksums(r io.Reader) map[string]string {
	out := map[string]string{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := bsdChecksumLine.FindStringSubmatch(line); m != nil {
			out[path.Base(strings.TrimSpace(m[1]))] = strings.ToLower(m[2])
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		sum, err := NormaliseSHA256(fields[0])
		if err != nil {
			continue
		}
		name := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		out[path.Base(name)] = sum
	}
	return out
}

func LookupChecksum(sums map[string]string, filename string) (string, bool) {
	if sum, ok := sums[filename]; ok {
		return sum, true
	}
	lower := strings.ToLower(filename)
	for name, sum := range sums {
		if strings.ToLower(name) == lower {
			return sum, true
		}
	}
	return "", false
}

func FetchChecksum(ctx context.Context, checksumURL, filename string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Bootimus PXE Server")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum file returned HTTP %d", resp.StatusCode)
	}
	sums := ParseChecksums(io.LimitReader(resp.Body, 4<<20))
	if sum, ok := LookupChecksum(sums, filename); ok {
		return sum, nil
	}
	return "", ErrChecksumNotFound
}

func ChecksumURLFor(isoURL, checksumPath string) string {
	if checksumPath == "" {
		return ""
	}
	if strings.Contains(checksumPath, "://") {
		return checksumPath
	}
	base, err := url.Parse(isoURL)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(checksumPath, "/") {
		base.Path = checksumPath
		base.RawQuery = ""
		return base.String()
	}
	base.Path = path.Join(path.Dir(base.Path), checksumPath)
	base.RawQuery = ""
	return base.String()
}

func DiscoverChecksum(ctx context.Context, isoURL, filename string) (string, string, error) {
	var lastErr error = ErrChecksumNotFound
	for _, candidate := range ChecksumFileCandidates {
		candidateURL := ChecksumURLFor(isoURL, candidate)
		if candidateURL == "" {
			continue
		}
		sum, err := FetchChecksum(ctx, candidateURL, filename)
		if err == nil {
			return sum, candidateURL, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
	}
	if errors.Is(lastErr, ErrChecksumNotFound) {
		return "", "", lastErr
	}
	return "", "", fmt.Errorf("%w (last attempt: %v)", ErrChecksumNotFound, lastErr)
}
