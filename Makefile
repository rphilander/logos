BINS = core/logos core/cmd/logos-cli/logos-cli \
       mcp-logos/mcp-logos \
       mod-time/mod-time mod-http-server/mod-http-server \
       mod-sqlite/mod-sqlite mod-mcp-server/mod-mcp-server \
       mod-fs/mod-fs

all: fmt test $(BINS)

fmt:
	gofmt -w core/ mcp-logos/ mod-time/ mod-http-server/ mod-sqlite/ mod-mcp-server/ mod-fs/

test:
	cd core && go test ./...
	cd mod-time && go test ./...

core/logos: core/*.go
	cd core && go build -o logos ./cmd/logos

core/cmd/logos-cli/logos-cli: core/*.go core/cmd/logos-cli/*.go
	cd core && go build -o cmd/logos-cli/logos-cli ./cmd/logos-cli

mcp-logos/mcp-logos: mcp-logos/*.go
	cd mcp-logos && go build -o mcp-logos .

mod-time/mod-time: mod-time/*.go
	cd mod-time && go build -o mod-time .

mod-http-server/mod-http-server: mod-http-server/*.go
	cd mod-http-server && go build -o mod-http-server .

mod-sqlite/mod-sqlite: mod-sqlite/*.go
	cd mod-sqlite && go build -o mod-sqlite .

mod-mcp-server/mod-mcp-server: mod-mcp-server/*.go
	cd mod-mcp-server && go build -o mod-mcp-server .

mod-fs/mod-fs: mod-fs/*.go
	cd mod-fs && go build -o mod-fs .

clean:
	rm -f $(BINS)

.PHONY: all fmt test clean
