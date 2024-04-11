VERSION := $(shell git describe --tags)
BUILD_TIME := $(shell date +%FT%T%z)
.PHONY: build

build:
	go build -ldflags "-w -X main.BuildTime=${BUILD_TIME} -X main.Version=${VERSION}" .
