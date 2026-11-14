package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wantemira/e2-engineers/backend/internal/demo"
)

func main() {
	root := flag.String("root", "..", "project root containing mocks")
	dataDir := flag.String("data", os.Getenv("DATA_DIR"), "runtime data directory (default <root>/.data)")
	revision := flag.String("revision", "", "select initial or fixed PR")
	checks := flag.String("checks", "", "set UNKNOWN, RUNNING, FAILED or PASSED for the selected PR")
	environment := flag.String("environment", "", "set STOPPED, STARTING, READY or FAILED for the mock training environment")
	reset := flag.Bool("reset", false, "restore initial PR and unknown CI for all revisions")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(1)
	}
	if *dataDir == "" {
		*dataDir = filepath.Join(*root, ".data")
	}
	var err error
	if *environment != "" {
		if *revision != "" || *checks != "" || *reset {
			fmt.Fprintln(os.Stderr, "environment is used without revision, checks or reset")
			os.Exit(1)
		}
		err = demo.SetEnvironment(*dataDir, *environment)
	} else {
		err = demo.Configure(*root, *dataDir, *revision, *checks, *reset)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("Демонстрационные данные обновлены. Запросите состояние в чате.")
}
