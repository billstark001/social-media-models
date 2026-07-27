.PHONY: build test benchmark fmt clean

GO ?= go

build:
	$(GO) build -o ./smp ./cmd/smp

test:
	$(GO) test ./...

benchmark:
	$(GO) test ./... -run '^$$' -bench . -benchmem

fmt:
	$(GO) fmt ./...

clean:
	$(RM) ./smp ./smp.exe
