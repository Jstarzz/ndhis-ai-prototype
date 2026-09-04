FROM node:22-bookworm-slim
WORKDIR /app
COPY package.json .
RUN npm install --omit=dev
COPY index.mjs .
EXPOSE 8104
CMD ["node", "index.mjs"]
