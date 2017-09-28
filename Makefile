
OUT_DIR = _output
export OUT_DIR

.PHONY: build

build:
	go build -v -o ${OUT_DIR}/ovnctl cmd/ovnctl/ovnctl.go
	go build -v -o ${OUT_DIR}/ovn-cni cmd/ovncni/ovn-cni.go

install:
	cp ${OUT_DIR}/* /usr/bin/

clean:
	rm -rf ${OUT_DIR}

.PHONY: check-gopath install.tools lint gofmt

check-gopath:
ifndef GOPATH
	$(error GOPATH is not set)
endif

install.tools: check-gopath
	go get -u gopkg.in/alecthomas/gometalinter.v1; \
	$(GOPATH)/bin/gometalinter.v1 --install;

lint:
	@./hack/lint.sh

gofmt:
	@./hack/verify-gofmt.sh
