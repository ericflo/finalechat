import datetime
import time
import traceback
import json
import multiprocessing

from vllm import LLM, SamplingParams

from minichat_convo import get_prompt
from client import DEFAULT_CLIENT


def get_sampling_params(data):
    return SamplingParams(
        n=data.get("n", 1),
        best_of=data.get("best_of", 1),
        presence_penalty=data.get("presence_penalty", 0.0),
        frequency_penalty=data.get("frequency_penalty", 0.0),
        repetition_penalty=data.get("repetition_penalty", 1.0),
        temperature=data.get("temperature", 0.2),
        top_p=data.get("top_p", 1.0),
        top_k=data.get("top_k", -1),
        min_p=data.get("min_p", 0.0),
        use_beam_search=data.get("use_beam_search", False),
        length_penalty=data.get("length_penalty", 1.0),
        early_stopping=data.get("early_stopping", False),
        stop=data.get("stop", None),
        stop_token_ids=data.get("stop_token_ids", None),
        ignore_eos=data.get("ignore_eos", False),
        max_tokens=data.get("max_tokens", 1024),
        logprobs=data.get("logprobs", None),
        prompt_logprobs=data.get("prompt_logprobs", None),
        skip_special_tokens=data.get("skip_special_tokens", True),
        spaces_between_special_tokens=data.get("spaces_between_special_tokens", True),
    )


def get_llm_kwargs(model, llm_params):
    default_llm_params = {
        "tokenizer_mode": "auto",
        "trust_remote_code": False,
        "tensor_parallel_size": 1,
        "dtype": "auto",
        "seed": 0,
        "gpu_memory_utilization": 0.9,
        "swap_space": 4,
    }
    resp = {"model": model}
    data = json.loads(llm_params) if llm_params else {}
    if "tokenizer" in data:
        resp["tokenizer"] = data["tokenizer"]
    resp["tokenizer_mode"] = data.get(
        "tokenizer_mode", default_llm_params["tokenizer_mode"]
    )
    resp["trust_remote_code"] = data.get(
        "trust_remote_code", default_llm_params["trust_remote_code"]
    )
    resp["tensor_parallel_size"] = data.get(
        "tensor_parallel_size", default_llm_params["tensor_parallel_size"]
    )
    resp["dtype"] = data.get("dtype", default_llm_params["dtype"])
    if "quantization" in data:
        resp["quantization"] = data["quantization"]
    if "revision" in data:
        resp["revision"] = data["revision"]
    if "tokenizer_revision" in data:
        resp["tokenizer_revision"] = data["tokenizer_revision"]
    resp["seed"] = data.get("seed", default_llm_params["seed"])
    resp["gpu_memory_utilization"] = data.get(
        "gpu_memory_utilization", default_llm_params["gpu_memory_utilization"]
    )
    resp["swap_space"] = data.get("swap_space", default_llm_params["swap_space"])
    return resp


def _sort_chats(chats):
    return sorted(
        chats, key=lambda c: datetime.datetime.fromisoformat(c["created_timestamp"])
    )


def _sort_messages(messages):
    return sorted(
        messages, key=lambda m: datetime.datetime.fromisoformat(m["timestamp"])
    )


def process_chat(
    chat_id, model, llm_params, sampling_params_json, task_queue, result_queue
):
    try:
        llm = LLM(**get_llm_kwargs(model=model, llm_params=llm_params))
        sampling_params = get_sampling_params(json.loads(sampling_params_json))

        while True:
            task = task_queue.get()  # Blocking call
            if task == "TERMINATE":
                result_queue.put((chat_id, "terminated"))
                break  # Gracefully exit the loop for termination

            chat, messages = task
            if chat["model"] != model:
                result_queue.put((chat_id, "model_change"))
                break  # Exit if model has changed

            DEFAULT_CLIENT.update_chat(chat_id, status="processing")
            prompt = get_prompt(model_name=model, messages=messages)
            response = llm.generate([prompt], sampling_params=sampling_params)[0]
            output = "\n".join([o.text for o in response.outputs])
            DEFAULT_CLIENT.create_message(chat_id, output, "assistant")
            result_queue.put((chat_id, "success"))
    except Exception as e:
        traceback.print_exc()  # Improved error logging
        result_queue.put((chat_id, "error", str(e)))
    finally:
        DEFAULT_CLIENT.update_chat(chat_id, status="idle")


def main():
    round = 0
    result_queue = multiprocessing.Queue()
    worker_processes = {}
    worker_task_queues = {}
    worker_models = {}  # Track models for each chat

    while True:
        round += 1
        print(f"Round {round}")
        try:
            chats = _sort_chats(DEFAULT_CLIENT.get_chats())
            if chats is not None:
                for chat in chats:
                    chat_id = chat["id"]
                    messages = _sort_messages(DEFAULT_CLIENT.get_messages(chat_id))

                    if (
                        not messages
                        or messages[-1]["sender_type"] != "user"
                        or chat["status"] != "idle"
                    ):
                        continue

                    current_model = chat["model"]
                    if (
                        chat_id not in worker_processes
                        or worker_models.get(chat_id) != current_model
                    ):
                        if chat_id in worker_processes:
                            worker_task_queues[chat_id].put("TERMINATE")
                            worker_processes[chat_id].join()
                            del worker_processes[chat_id]
                            del worker_task_queues[chat_id]

                        task_queue = multiprocessing.Queue()
                        p = multiprocessing.Process(
                            target=process_chat,
                            args=(
                                chat_id,
                                current_model,
                                chat["llm_params"],
                                chat["sampling_params"],
                                task_queue,
                                result_queue,
                            ),
                        )
                        p.start()
                        worker_processes[chat_id] = p
                        worker_task_queues[chat_id] = task_queue
                        worker_models[chat_id] = current_model

                    worker_task_queues[chat_id].put((chat, messages))

        except Exception as e:
            traceback.print_exception(e)

        # Handling results from subprocesses
        while not result_queue.empty():
            chat_id, status, error_message = result_queue.get()
            if status == "error":
                print(f"Error processing chat {chat_id}: {error_message}")
            elif status == "terminated":
                print(f"Chat {chat_id} process terminated.")
            elif status == "model_change":
                print(f"Model change detected for chat {chat_id}.")

        time.sleep(1.0)


if __name__ == "__main__":
    main()
