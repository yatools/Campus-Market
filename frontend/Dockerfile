FROM node:22-alpine AS build
WORKDIR /src
COPY frontend/package*.json ./
RUN npm ci --no-audit --no-fund
# 只复制构建真正需要的内容，而不是整个 frontend/。`COPY frontend/ ./` 会把宿主机的
# node_modules 一起盖到上一层 npm ci 的结果之上（.dockerignore 是第一道防线，
# 这里是第二道），跨平台构建会因原生依赖不匹配直接失败，缓存也整层失效。
COPY frontend/index.html frontend/tsconfig*.json frontend/vite.config.ts ./
COPY frontend/src ./src
RUN npm run build

FROM nginx:stable-alpine
COPY --from=build /src/dist /usr/share/nginx/html
COPY deploy/default.conf.template /etc/nginx/templates/default.conf.template
RUN apk upgrade --no-cache \
    && rm -f /etc/nginx/conf.d/default.conf
EXPOSE 80 443
