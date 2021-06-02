FROM golang:1.16 as builder
WORKDIR /go/src/csi-driver
ENV CGO_ENABLED=0

# Download dependencies
COPY go.* ./
RUN go mod download -x

# Build application
COPY . .
RUN go install ./...

FROM alpine:3.12 as app
RUN apk add --update e2fsprogs e2fsprogs-extra blkid util-linux
COPY --from=builder /go/bin/csi-driver /bin/csi-driver

ENTRYPOINT ["/bin/csi-driver"]
