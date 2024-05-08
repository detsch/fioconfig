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

	"github.com/gin-gonic/gin"

	"github.com/detsch/go-tuf/v2/metadata"
	"github.com/detsch/go-tuf/v2/metadata/config"
	"github.com/detsch/go-tuf/v2/metadata/updater"
	"github.com/go-logr/stdr"
	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)
var (
	globalApp *App
)

type FioFetcher struct {
	client *http.Client
	tag string
	repoUrl string
}

// type LocalFetcher struct {
// 	client *http.Client
// 	tag string
// 	repoUrl string
// }

func readLocalFile(filePath string ) ([]byte, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, &metadata.ErrDownloadHTTP{StatusCode: 404, URL: "file://" + filePath}
	}
	return data, nil
}

// DownloadFile downloads a file from urlPath, errors out if it failed,
// its length is larger than maxLength or the timeout is reached.
func (d *FioFetcher) DownloadFile(urlPath string, maxLength int64, timeout time.Duration) ([]byte, error) {
	if strings.HasPrefix(urlPath, "file://") {
		return readLocalFile(urlPath[len("file://"):])
	}

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

func (a *App) RefreshTuf(localRepoPath string) error {
	globalApp = a
	client, crypto := createClient(a.sota)
	// defer crypto.Close()
	a.callInitFunctions(client, crypto)

	return a.refreshTuf(client, localRepoPath)
}

func getTufCfg(repoUrl string, client *http.Client) (*config.UpdaterConfig, error) {
	localMetadataDir := "/var/sota/tuf/"
	rootBytes, err := os.ReadFile(filepath.Join(localMetadataDir, "root.json"))
	if err != nil {
		log.Println("os.ReadFile error")
		return nil, err
	}

	// create updater configuration
	cfg, err := config.New(repoUrl, rootBytes) // default config
	if err != nil {
		log.Println("config.New(repoUrl, error")
		return nil, err
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
	return cfg, nil
}

var (
	fioUpdater *updater.Updater
	fioClient *http.Client
)

func (a *App) refreshTuf(client *http.Client, localRepoPath string) error {
	metadata.SetLogger(stdr.New(stdlog.New(os.Stdout, "fioconfig", stdlog.LstdFlags)))
	fioClient = client

	var repoUrl string
	if localRepoPath == "" {
		repoUrl = strings.Replace(a.configUrl, "/config", "/repo", -1)
	} else {
		repoUrl = localRepoPath
	}
	// headers := make(map[string]string)
	// headers["x-ats-tags"] = "main"
	// res, err := httpGet(client, repoUrl + "/targets.json", headers)
	// if err != nil {
	// 	log.Println("Unable to attempt request")
	// 	return err // Unable to attempt request
	// }

	cfg, err := getTufCfg(repoUrl, client)
	if err != nil {
		log.Println("failed to create Config instance: %w", err)
		return err
	}

	// create a new Updater instance
	up, err := updater.New(cfg)
	if err != nil {
		log.Println("failed to create Updater instance: %w", err)
		return err
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
	fioUpdater = up

	log.Println("DONE ONLINE")

	// startDbus()
	startHttpServer()
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



const intro = `
<node>
	<interface name="com.github.guelfey.Demo">
		<method name="Foo">
			<arg direction="out" type="s"/>
		</method>
		<method name="Sleep">
			<arg direction="in" type="u"/>
		</method>
	</interface>` + introspect.IntrospectDataString + `</node> `

type foo string

// func (f foo) ReadRemotePath(remoteTufRepo string) ([]string, *dbus.Error) {
// 	ret := []string{}
// 	targets := fioUpdater.GetTopLevelTargets()
// 	for name, _ := range targets {
// 		t, _ := targets[name].MarshalJSON()
// 		ret = append(ret, string(t))
// 	}

// 	return ret, nil
// }

func (f foo) ReadLocalPath(localTufRepo string) (int, *dbus.Error) {
	log.Println("ReadLocalPath BEGIN " + localTufRepo)
	cfg, err := getTufCfg(localTufRepo, fioClient)
	if err != nil {
		log.Println("failed to create Config instance: %w", err)
		return -1, nil
	}

	// create a new Updater instance
	up, err := updater.New(cfg)
	if err != nil {
		log.Println("failed to create Updater instance: %w", err)
		return -1, nil
	}

	// try to build the top-level metadata
	err = up.Refresh()
	if err != nil {
		log.Println("failed to refresh trusted metadata: %w", err)
		return -1, nil
	}
	return 0, nil
}

func (f foo) GetTargets() ([]string, *dbus.Error) {
	ret := []string{}
	targets := fioUpdater.GetTopLevelTargets()
	for name, _ := range targets {
		t, _ := targets[name].MarshalJSON()
		ret = append(ret, string(t))
	}

	return ret, nil
}

func (f foo) Sleep(seconds uint) *dbus.Error {
	fmt.Println("Sleeping", seconds, "second(s)")
	time.Sleep(time.Duration(seconds) * time.Second)
	return nil
}

func startDbus() {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	f := foo("Bar!")
	conn.Export(f, "/io/foundries/tuf", "io.foundries.tuf")
	conn.Export(introspect.Introspectable(intro), "/io/foundries/tuf",
		"org.freedesktop.DBus.Introspectable")

	reply, err := conn.RequestName("io.foundries.tuf",
		dbus.NameFlagDoNotQueue)
	if err != nil {
		panic(err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		fmt.Fprintln(os.Stderr, "name already taken")
		os.Exit(1)
	}
	fmt.Println("Listening on io.foundries.tuf / /io/foundries/tuf ...")
	select {}
}

func GetTargetsHttp(c *gin.Context) {
	ret := []string{}
	targets := fioUpdater.GetTopLevelTargets()
	for name, _ := range targets {
		t, _ := targets[name].MarshalJSON()
		ret = append(ret, string(t))
	}

	c.IndentedJSON(http.StatusOK, targets)
}


type tufError struct {
	s string
}

func (f tufError) Error() string {
	return "TUF error"
}


func UpdateTargets(c *gin.Context) {

	log.Println("UpdateTargets BEGIN")
	globalApp.refreshTuf(fioClient, "")
	c.Done()
	log.Println("UpdateTargets END")
}

func ReadLocalPathHttp(c *gin.Context) {
	localTufRepo := c.Param("localTufRepo")
	log.Println("ReadLocalPathHttp BEGIN " + localTufRepo)
	cfg, err := getTufCfg(localTufRepo, fioClient)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, tufError{ fmt.Sprintf("failed to create Config instance: %w", err)})
	}

	// create a new Updater instance
	up, err := updater.New(cfg)
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, tufError{ fmt.Sprintf("failed to create Updater instance: %w", err)})
	}

	// try to build the top-level metadata
	err = up.Refresh()
	if err != nil {
		c.AbortWithError(http.StatusBadRequest, tufError{ fmt.Sprintf("failed to refresh trusted metadata: %w", err)})
	}
	c.Done()
	log.Println("ReadLocalPathHttp END " + localTufRepo)
}

func startHttpServer() {
	router := gin.Default()
	router.GET("/targets", GetTargetsHttp)
	// Just test routes for now. Those would probably be POST methods
	router.GET("/targets/update", UpdateTargets)
	router.GET("/targets/update_local/:localTufRepo", ReadLocalPathHttp)
	fmt.Println("Starting test http server at port 9080")
	router.Run("localhost:9080")
	fmt.Println("Exit from port 9080")
}
