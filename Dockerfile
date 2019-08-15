FROM golang:1.12 as builder
COPY . /go/src/github.com/apache/cloudstack-kubernetes-provider
WORKDIR /go/src/github.com/apache/cloudstack-kubernetes-provider
RUN  make clean && CGO_ENABLED=0 GOOS=linux make

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/src/github.com/apache/cloudstack-kubernetes-provider .
CMD ["./cloudstack-ccm", "--cloud-provider", "external-cloudstack"]
