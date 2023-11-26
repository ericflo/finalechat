import os
import shutil
import re
import subprocess

from huggingface_hub import snapshot_download


def download_model(model_id, models_dir, force=False):
    dirname = re.sub(r"[^\w-]", "_", model_id)
    output_dir = os.path.join(models_dir, dirname)
    if os.path.exists(output_dir):
        if force:
            print(f"Model '{model_id}' already exists at '{output_dir}'.")
            print("Deleting and re-downloading")
            shutil.rmtree(output_dir)
        else:
            print(f"Model '{model_id}' already exists at '{output_dir}'.")
            return output_dir
    os.makedirs(output_dir, exist_ok=True)
    snapshot_download(model_id, local_dir=output_dir)
    return output_dir


if __name__ == "__main__":
    models_dir = "tmp-models"
    os.makedirs(models_dir, exist_ok=True)
    output_dir = download_model("microsoft/Orca-2-7b", models_dir)
    subprocess.run(["python", "convert-hf-to-gguf.py", f"{output_dir}"])
