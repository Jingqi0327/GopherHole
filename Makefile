.PHONY: server client proto build build-all build-linux build-darwin build-windows clean

proto:
	rm -rf proto/pb/*.go
	protoc --go_out=proto/pb --go_opt=paths=source_relative \
	    --go-grpc_out=proto/pb --go-grpc_opt=paths=source_relative \
	    -I proto proto/signaling.proto

server:
	go run cmd/server/main.go

client:
	go run cmd/client/main.go

build:
	mkdir -p bin
	go build -o bin/server cmd/server/main.go
	go build -o bin/client cmd/client/main.go

build-all: build-linux build-darwin build-windows

build-linux:
	mkdir -p bin
	GOOS=linux GOARCH=amd64 go build -o bin/server-linux-amd64 cmd/server/main.go
	GOOS=linux GOARCH=amd64 go build -o bin/client-linux-amd64 cmd/client/main.go

build-darwin:
	mkdir -p bin
	GOOS=darwin GOARCH=amd64 go build -o bin/server-darwin-amd64 cmd/server/main.go
	GOOS=darwin GOARCH=amd64 go build -o bin/client-darwin-amd64 cmd/client/main.go
	GOOS=darwin GOARCH=arm64 go build -o bin/server-darwin-arm64 cmd/server/main.go
	GOOS=darwin GOARCH=arm64 go build -o bin/client-darwin-arm64 cmd/client/main.go

build-windows:
	mkdir -p bin
	GOOS=windows GOARCH=amd64 go build -o bin/server-windows-amd64.exe cmd/server/main.go
	GOOS=windows GOARCH=amd64 go build -o bin/client-windows-amd64.exe cmd/client/main.go

clean:
	rm -rf bin/
