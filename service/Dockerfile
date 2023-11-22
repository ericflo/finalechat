FROM python:3.12-slim

ENV FINALECHAT_ROOT_DIR /etc/finalechat
ENV SERVICE_DIR ${FINALECHAT_ROOT_DIR}/service
ENV FINALECHAT_API_URL https://api.finalechat.com

RUN mkdir -p ${SERVICE_DIR}
WORKDIR ${SERVICE_DIR}

COPY requirements.txt ${SERVICE_DIR}/requirements.txt
RUN pip install -r ${SERVICE_DIR}/requirements.txt

COPY . ${SERVICE_DIR}/

EXPOSE 7025
CMD [ "python", "app.py" ]