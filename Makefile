.PHONY: build clean

build:
	go build -ldflags "-X main.version=$$(git describe --tags --always 2>/dev/null || echo dev)" -o ios-screencast .

clean:
	rm -f ios-screencast
