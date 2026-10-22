FROM node:22-alpine AS build

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY server ./server
COPY shared ./shared
RUN npm run server:build

FROM node:22-alpine AS runtime

ENV NODE_ENV=production
WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts
COPY --from=build /app/dist/server ./dist/server
COPY --from=build /app/dist/shared ./dist/shared

EXPOSE 3000

CMD ["node", "dist/server/src/main.js"]
