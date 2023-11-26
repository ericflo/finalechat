import os
import requests


class ChatAPIClient:
    def __init__(self, base_url):
        self.base_url = base_url
        self.headers = {
            "Content-Type": "application/json",
            "X-Secret-Token": os.environ.get("FINALECHAT_SECRET_TOKEN", ""),
        }

    def update_chat(self, chat_id, summary=None, status=None):
        data = {}
        if summary is not None:
            data["summary"] = summary
        if status is not None:
            data["status"] = status
        response = requests.put(
            f"{self.base_url}/chats/{chat_id}", json=data, headers=self.headers
        )
        return response.json()

    def create_message(self, chat_id, text, sender_type):
        data = {"text": text, "sender_type": sender_type}
        response = requests.post(
            f"{self.base_url}/chats/{chat_id}/messages", json=data, headers=self.headers
        )
        return response.json()

    def update_message(self, message_id, text):
        data = {"text": text}
        response = requests.put(f"{self.base_url}/messages/{message_id}", json=data)
        return response.json()

    def get_next_workitems(self, model=None):
        response = requests.get(
            f"{self.base_url}/workitems/next",
            params={"model": model} if model else None,
            headers=self.headers,
        )
        return response.json()["items"]


FINALECHAT_API_URL = os.environ.get("FINALECHAT_API_URL", "http://localhost:7025")
DEFAULT_CLIENT = ChatAPIClient(FINALECHAT_API_URL)
