import subprocess
import datetime
import time
from client import DEFAULT_CLIENT

# Duration to wait before shutting down an idle worker (30 minutes)
IDLE_TIMEOUT = 1800


class WorkerManager:
    def __init__(self):
        self.workers = {}  # Dictionary to store subprocesses

    def start_worker(self, model):
        """Start a new worker subprocess for a given model."""
        if (
            model not in self.workers
            or self.workers[model]["process"].poll() is not None
        ):
            command = ["python", "model_worker.py", "--model", model]
            process = subprocess.Popen(command)
            self.workers[model] = {
                "process": process,
                "last_active": datetime.datetime.now(),
            }
            print(f"Started a new worker for model '{model}'.")

    def update_last_active(self, model):
        """Update the last active time of a worker."""
        if model in self.workers:
            self.workers[model]["last_active"] = datetime.datetime.now()

    def remove_idle_workers(self):
        """Shut down workers that have been idle for more than IDLE_TIMEOUT."""
        current_time = datetime.datetime.now()
        for model, worker in list(self.workers.items()):
            if (current_time - worker["last_active"]).total_seconds() > IDLE_TIMEOUT:
                worker["process"].terminate()
                del self.workers[model]
                print(f"Shut down worker for model '{model}' due to inactivity.")


def main():
    worker_manager = WorkerManager()

    while True:
        chats = DEFAULT_CLIENT.get_chats().get("items", [])
        for chat in chats:
            model = chat["model"]
            messages = DEFAULT_CLIENT.get_messages(chat["id"], order="asc").get(
                "items", []
            )

            if (
                messages
                and messages[-1]["sender_type"] == "user"
                and chat["status"] == "idle"
            ):
                worker_manager.start_worker(model)
                worker_manager.update_last_active(model)

        worker_manager.remove_idle_workers()
        time.sleep(1.0)  # Check for new chats and manage workers every second


if __name__ == "__main__":
    main()
