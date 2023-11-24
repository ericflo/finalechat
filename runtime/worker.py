import subprocess
import datetime
import time
from client import DEFAULT_CLIENT


class WorkerManager:
    def __init__(self, max_workers):
        self.workers = {}  # Dictionary to store subprocesses
        self.max_workers = max_workers

    def get_worker_count(self):
        """Return the current number of active workers."""
        return len(
            [
                worker
                for worker in self.workers.values()
                if worker["process"].poll() is None
            ]
        )

    def terminate_oldest_worker(self):
        """Terminate the oldest worker process."""
        oldest_model = None
        oldest_time = datetime.datetime.now()

        for model, worker in self.workers.items():
            if worker["last_active"] < oldest_time:
                oldest_time = worker["last_active"]
                oldest_model = model

        if oldest_model:
            self.workers[oldest_model]["process"].terminate()
            del self.workers[oldest_model]
            print(f"Terminated oldest worker for model '{oldest_model}'.")

    def start_worker(self, model):
        """Start a new worker subprocess for a given model."""
        # Check if a new worker is needed
        worker_needed = (
            model not in self.workers
            or self.workers[model]["process"].poll() is not None
        )

        # If a new worker is needed and worker count exceeds the limit, terminate the oldest worker
        if worker_needed and self.get_worker_count() >= self.max_workers:
            self.terminate_oldest_worker()

        # Start a new worker if needed
        if worker_needed:
            command = ["python", "model_worker.py", "--model", model]
            process = subprocess.Popen(command)
            self.workers[model] = {
                "process": process,
                "last_active": datetime.datetime.now(),
            }
            print(f"Started a new worker for model '{model}'.")


def main():
    worker_manager = WorkerManager(max_workers=1)

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
        time.sleep(1.0)  # Check for new chats and manage workers every second


if __name__ == "__main__":
    main()
