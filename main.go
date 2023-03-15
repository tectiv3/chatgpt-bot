package main

// main.go

import (
	"log"
	"os"
)

func main() {
	confFilepath := "config.json"
	if len(os.Args) == 2 {
		confFilepath = os.Args[1]
	}

	if conf, err := loadConfig(confFilepath); err == nil {
		runBot(conf)
	} else {
		log.Printf("failed to load config: %s", err)
	}

}
