FROM golang:1.25-alpine

RUN apk add --no-cache bash git build-base \
  && test -x /bin/bash

WORKDIR /app
