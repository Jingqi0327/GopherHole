.PHONY: server client proto build clean

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

clean:
	rm -rf bin/
