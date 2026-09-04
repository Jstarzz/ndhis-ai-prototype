FROM nvidia/cuda:12.8.1-cudnn-runtime-ubuntu24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-pip && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY requirements.txt .
RUN pip3 install --break-system-packages --no-cache-dir -r requirements.txt
COPY main.py .
EXPOSE 8101
CMD ["python3", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8101"]
