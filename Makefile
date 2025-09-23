BUILD_CMD = go build -o mpool-tx ./main.go

build:
	$(BUILD_CMD)

clean:
	rm -f mpool-tx