FROM pytorch/pytorch:2.1.0-cuda11.8-cudnn8-devel

ENV FINALECHAT_API_URL=https://api.finalechat.com

ENV TZ=America/Los_Angeles
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

RUN mkdir -p /etc/finalechat/runtime
WORKDIR /etc/finalechat/runtime

ADD requirements.txt /etc/finalechat/runtime/requirements.txt
RUN python3 -m pip install -r /etc/finalechat/runtime/requirements.txt

ADD . /etc/finalechat/runtime

CMD [ "python3", "/etc/finalechat/runtime/worker.py" ]