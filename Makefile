NAME?=kiam
ARCH=amd64
BIN = bin/kiam
BIN_LINUX = $(BIN)-linux-$(ARCH)
BIN_DARWIN = $(BIN)-darwin-$(ARCH)
GIT_BRANCH?=$(shell git rev-parse --abbrev-ref HEAD)
IMG_NAMESPACE?=quay.io/uswitch
IMG_TAG?=$(GIT_BRANCH)
REGISTRY?=$(IMG_NAMESPACE)/$(NAME)
SOURCES := $(shell find . -iname '*.go') proto/service.pb.go
export PATH := bin:$(PATH)

.PHONY: test clean all coverage

all: $(BIN_LINUX) $(BIN_DARWIN)

$(BIN_DARWIN): $(SOURCES)
	GOARCH=$(ARCH) GOOS=darwin go build -o $(BIN_DARWIN) cmd/kiam/*.go

$(BIN_LINUX): $(SOURCES)
	GOARCH=$(ARCH) GOOS=linux CGO_ENABLED=0 go build -o $(BIN_LINUX) cmd/kiam/*.go

.PHONY: generate
generate: clientset ## Generate code
#	go generate ./pkg/... ./cmd/...

.PHONY: clientset
clientset: ## Generate a typed clientset
	rm -rf pkg/k8s/client/*_generated
	go run ./vendor/k8s.io/code-generator/cmd/deepcopy-gen --input-dirs github.com/uswitch/kiam/pkg/apis/iam/v1alpha1 \
		--output-package github.com/uswitch/kiam/pkg/apis/iam/v1alpha1 \
		--go-header-file=./hack/boilerplate.go.txt
	go run ./vendor/k8s.io/code-generator/cmd/client-gen --clientset-name clientset --input-base github.com/uswitch/kiam/pkg/apis \
		--input iam/v1alpha1 --output-package github.com/uswitch/kiam/pkg/k8s/client/clientset_generated \
		--go-header-file=./hack/boilerplate.go.txt
	go run ./vendor/k8s.io/code-generator/cmd/lister-gen --input-dirs github.com/uswitch/kiam/pkg/apis/iam/v1alpha1 \
		--output-package github.com/uswitch/kiam/pkg/k8s/client/listers_generated \
		--go-header-file=./hack/boilerplate.go.txt
	go run ./vendor/k8s.io/code-generator/cmd/informer-gen --input-dirs github.com/uswitch/kiam/pkg/apis/iam/v1alpha1 \
		--versioned-clientset-package github.com/uswitch/kiam/pkg/k8s/client/clientset_generated/clientset \
		--listers-package github.com/uswitch/kiam/pkg/k8s/client/listers_generated \
		--output-package github.com/uswitch/kiam/pkg/k8s/client/informers_generated \
		--go-header-file=./hack/boilerplate.go.txt

deps:
	dep ensure

bin:
	mkdir -p bin

bin/protoc-gen-go: bin
	go build -o bin/protoc-gen-go ./vendor/github.com/golang/protobuf/protoc-gen-go

proto/service.pb.go: proto/service.proto bin/protoc-gen-go
	protoc -I proto/ proto/service.proto --go_out=plugins=grpc:proto

.PHONY: manifests
manifests: ## Generate manifests e.g. CRD, RBAC etc.
	go run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go crd
	go run vendor/sigs.k8s.io/controller-tools/cmd/controller-gen/main.go rbac --name kiam --service-account kiam-server --service-account-namespace kube-system

test: $(SOURCES)
	go test github.com/uswitch/kiam/pkg/... -race

coverage.txt: $(SOURCES)
	go test github.com/uswitch/kiam/pkg/... -coverprofile=coverage.txt -covermode=atomic

coverage: $(SOURCES) coverage.txt
	go tool cover -html=coverage.txt

bench: $(SOURCES)
	go test -run=XX -bench=. github.com/uswitch/kiam/pkg/...

docker: Dockerfile
	docker image build -t "$(REGISTRY):$(IMG_TAG)" .

push:
	docker push "$(REGISTRY):$(IMG_TAG)"

clean:
	rm -rf bin/
