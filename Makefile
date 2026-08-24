PLUGIN=cloudflare-tunnel

.PHONY: build test introspect clean
build:
	go build -o $(PLUGIN) .

test:
	go test ./...

introspect: build
	./$(PLUGIN) -introspect

clean:
	rm -f $(PLUGIN) $(PLUGIN).exe
