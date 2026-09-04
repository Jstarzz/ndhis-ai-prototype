FROM python:3.12-slim
WORKDIR /app
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
RUN pip install --no-cache-dir "timesfm[torch]==3.0.1"
COPY main.py .
EXPOSE 8102
CMD ["python", "-m", "uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8102"]
