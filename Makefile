.PHONY: build build-probe build-all test test-python benchmark benchmark-recsys fmt clean

GO ?= go
PYTHON ?= python

build:
	$(GO) build -o ./smp ./cmd/smp

build-probe:
	$(GO) build -o ./smp-probe ./cmd/smp-probe

build-all: build build-probe

test:
	$(GO) test ./...

test-python:
	$(PYTHON) -m unittest discover -s tests -p 'test_*.py'

benchmark:
	$(GO) test ./... -run '^$$' -bench . -benchmem

benchmark-recsys:
	$(GO) test ./recsys -run '^$$' -bench 'BenchmarkRecommend' -benchmem

fmt:
	$(GO) fmt ./...

clean:
	$(RM) ./smp ./smp.exe ./smp-probe ./smp-probe.exe
