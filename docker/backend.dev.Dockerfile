FROM golang:1.25.1-alpine3.22

RUN apk add --no-cache bash git build-base \
  && test -x /bin/bash

WORKDIR /app
