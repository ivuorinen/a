FROM alpine:latest
RUN apk --no-cache add ca-certificates
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/a /usr/local/bin/
ENTRYPOINT ["a"]
