.PHONY: build test vet smoke

build:
	mkdir -p build
	go build -trimpath -buildvcs=false -o build/seai ./cmd/seai
	go build -trimpath -buildvcs=false -o build/seaid ./cmd/seaid

test:
	go test -race -timeout 120s ./...

vet:
	go vet ./...

smoke: build
	python3 scripts/smoke.py --bin build
