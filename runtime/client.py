import requests
import json


class ChatAPIClient:
    def __init__(self, base_url):
        self.base_url = base_url

    def get_chats(self, page=1, per_page=100, order="desc"):
        response = requests.get(
            f"{self.base_url}/chats",
            params={"page": page, "per_page": per_page, "order": order},
        )
        return response.json()

    def create_chat(self, model="GeneZC/MiniChat-3B", sampling_params="{}"):
        response = requests.post(
            f"{self.base_url}/chats",
            params={"model": model, "sampling_params": sampling_params},
        )
        return response.json()

    def update_chat(self, chat_id, summary=None, status=None):
        data = {}
        if summary is not None:
            data["summary"] = summary
        if status is not None:
            data["status"] = status
        response = requests.put(f"{self.base_url}/chats/{chat_id}", json=data)
        return response.json()

    def get_chat(self, chat_id):
        response = requests.get(f"{self.base_url}/chats/{chat_id}")
        return response.json()

    def delete_chat(self, chat_id):
        response = requests.delete(f"{self.base_url}/chats/{chat_id}")
        return response.json()

    def get_messages(self, chat_id, page=1, per_page=100, order="desc"):
        response = requests.get(
            f"{self.base_url}/chats/{chat_id}/messages",
            params={"page": page, "per_page": per_page, "order": order},
        )
        return response.json()

    def create_message(self, chat_id, text, sender_type):
        data = {"text": text, "sender_type": sender_type}
        response = requests.post(f"{self.base_url}/chats/{chat_id}/messages", json=data)
        return response.json()

    def get_message(self, message_id):
        response = requests.get(f"{self.base_url}/messages/{message_id}")
        return response.json()

    def update_message(self, message_id, text):
        data = {"text": text}
        response = requests.put(f"{self.base_url}/messages/{message_id}", json=data)
        return response.json()

    def delete_message(self, message_id):
        response = requests.delete(f"{self.base_url}/messages/{message_id}")
        return response.json()


# DEFAULT_CLIENT = ChatAPIClient("http://localhost:7025")
DEFAULT_CLIENT = ChatAPIClient("https://buildassistant.ngrok.dev/api")
