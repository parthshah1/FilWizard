BUILD_CMD = go build -o filwizard ./main.go

build:
	$(BUILD_CMD)

clean:
	rm -f mpool-tx

.PHONY: docker
docker:
	docker build -t filwizard:latest .
