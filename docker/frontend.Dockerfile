FROM node:20.19.5-alpine3.22 AS builder

WORKDIR /app

ARG VITE_APP_VERSION=
ENV VITE_APP_VERSION=${VITE_APP_VERSION}

COPY frontend/package*.json ./
RUN npm ci

COPY frontend/ .

RUN npm run build


FROM nginx:1.29.1-alpine3.22

ENV NGINX_CLIENT_MAX_BODY_SIZE=32m
ENV NGINX_BACKEND_PORT=8080
ENV NGINX_TRUST_EXTERNAL_PROXY_HEADERS=false

RUN rm /etc/nginx/conf.d/default.conf && mkdir -p /etc/nginx/templates

COPY docker/nginx.conf /etc/nginx/templates/default.conf.template

COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 4173

CMD ["nginx", "-g", "daemon off;"]
