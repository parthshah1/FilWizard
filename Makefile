BUILD_CMD = go build -o filwizard ./main.go

build:
	$(BUILD_CMD)

clean:
	rm -f mpool-tx
