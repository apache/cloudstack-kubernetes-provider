FROM golang:1.12 as builder
COPY . /go/src/github.com/swisstxt/cloudstack-cloud-controller-manager
WORKDIR /go/src/github.com/swisstxt/cloudstack-cloud-controller-manager
RUN  make clean && CGO_ENABLED=0 GOOS=linux make

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /go/src/github.com/swisstxt/cloudstack-cloud-controller-manager/cloudstack-ccm .
CMD ["./cloudstack-ccm", "--cloud-provider", "external-cloudstack"]
