# epay-go/web/Dockerfile
# 构建阶段
FROM node:20-alpine AS builder

WORKDIR /app

# 复制 package.json
COPY package.json ./
RUN npm install

# 复制源代码并构建
COPY . .
RUN npm run build

# 运行阶段
FROM nginx:alpine
ENV NODE_ENV=production

# 复制构建产物
COPY --from=builder /app/dist /usr/share/nginx/html

# 复制 nginx 配置
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
