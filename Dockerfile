FROM node:22-alpine AS build

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY server ./server
COPY shared ./shared
RUN npm run server:build

FROM node:22-alpine AS runtime

WORKDIR /app

RUN apk add --no-cache ca-certificates && \
    wget -qO /etc/ssl/certs/rds-global-bundle.pem \
      https://truststore.pki.rds.amazonaws.com/global/global-bundle.pem && \
    test -s /etc/ssl/certs/rds-global-bundle.pem

COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY --from=build /app/.dev/server/server ./dist/server
COPY --from=build /app/.dev/server/shared ./dist/shared
COPY server/src/db/migrations ./server/src/db/migrations
COPY server/src/db/seeds ./server/src/db/seeds

USER node

EXPOSE 3000

CMD ["node", "dist/server/src/main.js"]
