package internal

import (
	"fmt"
	"log"
	stdlog "log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/detsch/go-tuf/v2/metadata"
	"github.com/detsch/go-tuf/v2/metadata/config"
	"github.com/detsch/go-tuf/v2/metadata/updater"
	"github.com/go-logr/stdr"
)


type FioFetcher struct {
	client *http.Client
	tag string
	repoUrl string
}

// DownloadFile downloads a file from urlPath, errors out if it failed,
// its length is larger than maxLength or the timeout is reached.
func (d *FioFetcher) DownloadFile(urlPath string, maxLength int64, timeout time.Duration) ([]byte, error) {
	headers := make(map[string]string)
	headers["x-ats-tags"] = d.tag
	res, err := httpGet(d.client, urlPath, headers)

	if err != nil {
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		return nil, &metadata.ErrDownloadHTTP{StatusCode: res.StatusCode, URL: urlPath}
	}

	fmt.Println("GET RESULT=" + string(res.Body))

	// client := &http.Client{Timeout: timeout}
	// req, err := http.NewRequest("GET", urlPath, nil)
	// if err != nil {
	// 	return nil, err
	// }
	// // Use in case of multiple sessions.
	// if d.httpUserAgent != "" {
	// 	req.Header.Set("User-Agent", d.httpUserAgent)
	// }
	// // Execute the request.
	// res, err := client.Do(req)
	// if err != nil {
	// 	return nil, err
	// }
	// defer res.Body.Close()
	// // Handle HTTP status codes.
	// if res.StatusCode != http.StatusOK {
	// 	return nil, &metadata.ErrDownloadHTTP{StatusCode: res.StatusCode, URL: urlPath}
	// }
	var length int64
	// Get content length from header (might not be accurate, -1 or not set).
	if header := res.Header.Get("Content-Length"); header != "" {
		length, err = strconv.ParseInt(header, 10, 0)
		if err != nil {
			return nil, err
		}
		// Error if the reported size is greater than what is expected.
		if length > maxLength {
			return nil, &metadata.ErrDownloadLengthMismatch{Msg: fmt.Sprintf("download failed for %s, length %d is larger than expected %d", urlPath, length, maxLength)}
		}
	}
	// // Although the size has been checked above, use a LimitReader in case
	// // the reported size is inaccurate, or size is -1 which indicates an
	// // unknown length. We read maxLength + 1 in order to check if the read data
	// // surpased our set limit.
	// data, err := io.ReadAll(io.LimitReader(res.Body, maxLength+1))
	// if err != nil {
	// 	return nil, err
	// }
	// Error if the reported size is greater than what is expected.
	length = int64(len(res.Body))
	if length > maxLength {
		return nil, &metadata.ErrDownloadLengthMismatch{Msg: fmt.Sprintf("download failed for %s, length %d is larger than expected %d", urlPath, length, maxLength)}
	}

	return res.Body, nil
}

func (a *App) RefreshTuf() error {
	client, crypto := createClient(a.sota)
	// defer crypto.Close()
	a.callInitFunctions(client, crypto)

	return a.refreshTuf(client)
}

func (a *App) refreshTuf(client *http.Client) error {
	metadata.SetLogger(stdr.New(stdlog.New(os.Stdout, "fioconfig", stdlog.LstdFlags)))

	repoUrl := strings.Replace(a.configUrl, "/config", "/repo", -1)
	// headers := make(map[string]string)
	// headers["x-ats-tags"] = "main"
	// res, err := httpGet(client, repoUrl + "/targets.json", headers)
	// if err != nil {
	// 	log.Println("Unable to attempt request")
	// 	return err // Unable to attempt request
	// }

	localMetadataDir := "/var/sota/"
	rootBytes, err := os.ReadFile(filepath.Join(localMetadataDir, "root.json"))
	if err != nil {
		log.Println("os.ReadFile error")
		return err
	}
	// create updater configuration
	cfg, err := config.New(repoUrl, rootBytes) // default config
	if err != nil {
		log.Println("config.New(repoUrl, error")
		return err
	}
	cfg.LocalMetadataDir = localMetadataDir
	cfg.LocalTargetsDir = filepath.Join(localMetadataDir, "download")
	cfg.RemoteTargetsURL = repoUrl
	cfg.PrefixTargetsWithHash = true
	cfg.Fetcher = &FioFetcher{
		client: client,
		tag: "main",
		repoUrl: repoUrl,
	}
	

	// create a new Updater instance
	up, err := updater.New(cfg)
	if err != nil {
		log.Println("failed to create Updater instance: %w", err)
		return nil
	}

	// try to build the top-level metadata
	err = up.Refresh()
	if err != nil {
		log.Println("failed to refresh trusted metadata: %w", err)
		return nil
	}
	for name, _ := range up.GetTopLevelTargets() {
		log.Println("target name" + name)
	}

	log.Println("DONE")

	return nil
}

func DieNotNil(err error, message ...string) {
	if err != nil {
		parts := []interface{}{"ERROR:"}
		for _, p := range message {
			parts = append(parts, p)
		}
		parts = append(parts, err)
		fmt.Println(parts...)
		os.Exit(1)
	}
}
