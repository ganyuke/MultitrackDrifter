SHELL := /bin/sh
BIN := bin/drifter

.PHONY: help deps web build run start test fmt clean demo-media zip

help:
	@echo "Targets: deps web build run start test fmt demo-media clean zip"

deps:
	go mod tidy
	go mod download
	cd web && npm install

web:
	cd web && npm run build
	rm -rf internal/webdist/dist
	mkdir -p internal/webdist
	cp -R web/dist internal/webdist/dist

build: web
	mkdir -p bin
	go build -o $(BIN) ./cmd/drifter

run:
	mkdir -p storage/source storage/hls
	go run ./cmd/drifter serve

start:
	./scripts/start-local.sh

test:
	go test ./...

fmt:
	gofmt -w cmd internal

# Creates tiny local MP4 source files with one video stream and three named audio streams.
demo-media:
	mkdir -p storage/source/Alice storage/source/Bob storage/hls
	ffmpeg -y -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=880:sample_rate=48000 -f lavfi -i sine=frequency=660:sample_rate=48000 -f lavfi -i sine=frequency=440:sample_rate=48000 -t 12 -map 0:v -map 1:a -map 2:a -map 3:a -metadata:s:v:0 handler_name="Gameplay" -metadata:s:a:0 handler_name="Game Audio" -metadata:s:a:1 handler_name="Mic" -metadata:s:a:2 handler_name="Discord" -pix_fmt yuv420p -c:v libx264 -preset ultrafast -c:a aac storage/source/Alice/gameplay.mp4
	ffmpeg -y -f lavfi -i testsrc=size=1280x720:rate=30 -f lavfi -i sine=frequency=770:sample_rate=48000 -f lavfi -i sine=frequency=550:sample_rate=48000 -f lavfi -i sine=frequency=330:sample_rate=48000 -t 12 -map 0:v -map 1:a -map 2:a -map 3:a -metadata:s:v:0 handler_name="Gameplay" -metadata:s:a:0 handler_name="Game Audio" -metadata:s:a:1 handler_name="Mic" -metadata:s:a:2 handler_name="Discord" -pix_fmt yuv420p -c:v libx264 -preset ultrafast -c:a aac storage/source/Bob/gameplay.mp4

clean:
	rm -rf bin web/dist internal/webdist/dist storage/*.db storage/*.db-* storage/hls/*

zip:
	cd .. && zip -qr multitrack-drifter-poc.zip multitrack-drifter-poc -x 'multitrack-drifter-poc/web/node_modules/*' 'multitrack-drifter-poc/bin/*' 'multitrack-drifter-poc/storage/*'
