VERSION ?= 0.1.0
BINARY = pigeon-claw
MODULE = github.com/tackish/pigeon-claw

.PHONY: build clean release test

build:
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/

release: clean
	mkdir -p dist
	# macOS ARM64
	GOOS=darwin GOARCH=arm64 go build -o dist/$(BINARY) .
	tar -czf dist/$(BINARY)-darwin-arm64.tar.gz -C dist $(BINARY)
	rm dist/$(BINARY)
	# macOS AMD64
	GOOS=darwin GOARCH=amd64 go build -o dist/$(BINARY) .
	tar -czf dist/$(BINARY)-darwin-amd64.tar.gz -C dist $(BINARY)
	rm dist/$(BINARY)
	# Print SHA256 for Homebrew formula
	@echo "\n=== SHA256 for Homebrew ==="
	@shasum -a 256 dist/*.tar.gz

install: build
	cp $(BINARY) /usr/local/bin/

uninstall:
	rm -f /usr/local/bin/$(BINARY)
