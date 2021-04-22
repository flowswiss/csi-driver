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
COPY --from=builder /go/bin/csi-driver /bin/csi-driver

ENTRYPOINT ["/bin/csi-driver"]
