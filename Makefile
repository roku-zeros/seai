.PHONY: build test vet smoke business-smoke mcp-smoke

build:
	mkdir -p build
	go build -trimpath -buildvcs=false -o build/seai ./cmd/seai
	go build -trimpath -buildvcs=false -o build/seaid ./cmd/seaid

test:
	go test -race -timeout 180s ./...

vet:
	go vet ./...

business-smoke: build
	python3 scripts/business_smoke.py --bin build

smoke: build
	python3 scripts/smoke.py --bin build

mcp-smoke: build
	python3 scripts/mcp_smoke.py --bin build
