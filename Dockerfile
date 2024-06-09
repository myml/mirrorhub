FROM golang as builder
COPY . /src
WORKDIR /src
RUN go build -o mirrorhub

FROM debian
COPY --from=builder /etc/ssl/certs /etc/ssl/certs
COPY --from=builder /src/mirrorhub /
ENTRYPOINT ["/mirrorhub"]