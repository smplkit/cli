# GoReleaser builds the binary, then copies it in here. This Dockerfile
# only repackages — it never `go build`s. Under dockers_v2 the build
# context holds the already-cross-compiled binaries laid out by
# $TARGETPLATFORM (e.g. linux/amd64/smplkit), so a single buildx run can
# assemble the multi-arch manifest from one Dockerfile.

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/smplkit /usr/local/bin/smplkit
ENTRYPOINT ["/usr/local/bin/smplkit"]
