# Build the manager binary
FROM registry.access.redhat.com/ubi9/go-toolset:1.21 as builder

# Bring in the go dependencies before anything else so we can take
# advantage of caching these layers in future builds.
COPY vendor/ vendor/

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Copy the go source
COPY cmd/ cmd/
COPY api/ api/
COPY internal/ internal/

# Build the binaries
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -mod=vendor -a -o build/manager cmd/main.go

#####################################################################################################
# Build the controller image
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

COPY --from=builder \
    /opt/app-root/src/build/manager \
    /usr/local/bin/

# Copy the licence
COPY LICENSE /licenses/LICENSE

ENV USER_UID=1001

USER ${USER_UID}

ENTRYPOINT ["/usr/local/bin/manager"]
