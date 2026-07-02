FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY a /usr/local/bin/
ENTRYPOINT ["a"]
