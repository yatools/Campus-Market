FROM node:22-alpine AS build
WORKDIR /src
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

FROM nginx:stable-alpine
COPY --from=build /src/dist /usr/share/nginx/html
COPY deploy/default.conf.template /etc/nginx/templates/default.conf.template
RUN apk upgrade --no-cache \
    && rm -f /etc/nginx/conf.d/default.conf
EXPOSE 80 443
