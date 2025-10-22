FROM node:24-trixie-slim

WORKDIR /app

COPY package.json package-lock.json* ./

RUN npm ci

COPY src ./src

RUN npx -y playwright@1.55.1 install --with-deps chromium

EXPOSE 3000

CMD ["npm", "start"]