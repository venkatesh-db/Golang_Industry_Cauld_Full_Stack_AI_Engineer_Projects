.PHONY: test race vet demo tidy

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race -cover ./...

# End-to-end demo: self-signed cert + live server + curl through all five features.
demo:
	./scripts/demo.sh

tidy:
	go mod tidy
