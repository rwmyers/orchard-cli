.PHONY: fmt vet test lint example check

# The examples are outside the main module, so the ./... targets below do not
# reach them. They are checked explicitly, since they are what breaks when the
# vcs interface or the plugin protocol changes.
GO_EXAMPLE     := examples/custom-binary
PLUGIN_EXAMPLE := examples/orchard-vcs-demo/orchard-vcs-demo

fmt:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; \
		echo "run: gofmt -w ."; \
		exit 1; \
	fi

vet:
	go vet ./...

test:
	go test ./...

lint:
	golangci-lint run

example:
	cd $(GO_EXAMPLE) && go vet ./... && go build ./...
	@python3 -c "import sys; compile(open(sys.argv[1]).read(), sys.argv[1], 'exec')" $(PLUGIN_EXAMPLE)
	@test -x $(PLUGIN_EXAMPLE) || { echo "$(PLUGIN_EXAMPLE) must be executable to be discovered on PATH"; exit 1; }
	@echo '{"api_version":1}' | ./$(PLUGIN_EXAMPLE) describe >/dev/null

check: fmt vet test example lint
