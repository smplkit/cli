# GoReleaser builds the binary, then copies it in here. This Dockerfile
# only repackages — it never `go build`s, so the build context is the
# directory containing the already-cross-compiled smplkit binary.

FROM gcr.io/distroless/static-debian12:nonroot
COPY smplkit /usr/local/bin/smplkit
ENTRYPOINT ["/usr/local/bin/smplkit"]
