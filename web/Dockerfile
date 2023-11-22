FROM node:21-bookworm-slim

ENV NODE_ENV production
ENV FINALECHAT_ROOT_DIR /etc/finalechat
ENV WEB_DIR ${FINALECHAT_ROOT_DIR}/web
ENV NEXT_PUBLIC_FINALECHAT_API_URL https://api.finalechat.com

RUN mkdir -p ${WEB_DIR}
WORKDIR ${WEB_DIR}

COPY package.json ${WEB_DIR}/package.json
RUN npm install

COPY . ${WEB_DIR}/

RUN npm run build

EXPOSE 3000
CMD [ "npm", "run", "start" ]