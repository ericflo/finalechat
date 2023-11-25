import argparse
import subprocess
import datetime
import time
import traceback
from typing import Optional
from client import DEFAULT_CLIENT

MIN_WORKER_LIFETIME = (
    60  # Minimum number of seconds a worker should run before being terminated
)


class WorkerManager:
    def __init__(
        self, max_workers, dtype, max_model_len, min_worker_lifetime=MIN_WORKER_LIFETIME
    ):
        self.workers = {}  # Dictionary to store subprocesses
        self.dtype = dtype
        self.max_model_len = max_model_len
        self.max_workers = max_workers
        self.min_worker_lifetime = min_worker_lifetime

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
            if worker["spawn_time"] < oldest_time:
                oldest_time = worker["spawn_time"]
                oldest_model = model

        # Check if the oldest worker has been running for `min_worker_lifetime` seconds
        time_diff = (datetime.datetime.now() - oldest_time).total_seconds()
        if oldest_model and time_diff > self.min_worker_lifetime:
            self.workers[oldest_model]["process"].terminate()
            del self.workers[oldest_model]
            print(f"Terminated oldest worker for model '{oldest_model}'.")
            return True
        else:
            print(
                f"Oldest worker has not yet reached min lifetime ({self.min_worker_lifetime} seconds)."
            )
            return False

    def start_worker(self, model):
        """Start a new worker subprocess for a given model."""
        # Check if a new worker is needed
        worker_needed = (
            model not in self.workers
            or self.workers[model]["process"].poll() is not None
        )

        # If a new worker is needed and worker count exceeds the limit, try to terminate the oldest worker
        if worker_needed and self.get_worker_count() >= self.max_workers:
            terminated = self.terminate_oldest_worker()
            if not terminated:
                # Calculate remaining time for the oldest worker to reach min lifetime and sleep
                oldest_time = min(
                    [worker["spawn_time"] for worker in self.workers.values()]
                )
                remaining_time = max(
                    0,
                    self.min_worker_lifetime
                    - (datetime.datetime.now() - oldest_time).total_seconds(),
                )
                time.sleep(remaining_time)

        # Start a new worker if needed
        if worker_needed:
            command = [
                "python",
                "model_worker.py",
                "--model",
                model,
                "--dtype",
                self.dtype,
            ]
            if self.max_model_len:
                command.extend(["--max_model_len", str(self.max_model_len)])
            process = subprocess.Popen(command)
            self.workers[model] = {
                "process": process,
                "spawn_time": datetime.datetime.now(),
            }
            print(f"Started a new worker for model '{model}'.")


def main(dtype: str, max_model_len: Optional[int]):
    worker_manager = WorkerManager(
        max_workers=1, dtype=dtype, max_model_len=max_model_len
    )

    while True:
        try:
            for workitem in DEFAULT_CLIENT.get_next_workitems():
                worker_manager.start_worker(workitem["chat"]["model"])
        except (KeyboardInterrupt, SystemExit):
            print("Exiting...")
            break
        except Exception as e:
            traceback.print_exception(e)
        time.sleep(1.0)  # Check for new chats and manage workers every second


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Manages workers for evaluating responses to chats"
    )
    parser.add_argument(
        "--dtype",
        help="Specify the data type",
        default="auto",
        choices=["auto", "float32", "float16", "bfloat16"],
    )
    parser.add_argument(
        "--max_model_len",
        help="Specify the maximum model length",
        default=None,
        type=int,
        required=False,
    )
    args = parser.parse_args()
    main(args.dtype, args.max_model_len)
