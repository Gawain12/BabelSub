package main

import (
	_ "embed"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/Gawain12/BabelSub/pkg/config"
)

//go:embed web/index.html
var indexHTML []byte

var version = "0.2.0"

func main() {
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		handleCLI()
		return
	}

	// Server mode
	port := flag.Int("port", 18980, "Web/API server port")
	listen := flag.String("listen", "0.0.0.0", "监听地址；只在本机使用可设为 127.0.0.1")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		log.Printf("Warning: failed to load config: %v", err)
		cfg = config.DefaultConfig()
	}

	s := newServer(cfg)

	addr := fmt.Sprintf("%s:%d", *listen, *port)
	fmt.Printf("====================================================================\n")
	fmt.Printf(" BabelSub (巴别字幕) v%s - 媒体库外语双语字幕系统\n", version)
	fmt.Printf(" 控制台地址: http://localhost:%d\n", *port)
	fmt.Printf(" 配置文件: %s\n", config.ConfigFilePath())
	fmt.Printf("====================================================================\n")

	if err := http.ListenAndServe(addr, s.routes()); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
