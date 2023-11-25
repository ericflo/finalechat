import React, { useCallback, useState } from "react";

import { MODEL_NAMES } from "../../constants";

export interface EmptyStateProps {
  modelName?: string;
  onModelChange?: (modelName: string) => void;
}

const EmptyState = ({ modelName, onModelChange }: EmptyStateProps) => {
  const [showTextBox, setShowTextBox] = useState(false);
  const [customModel, setCustomModel] = useState("");

  const handleModelChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      const selectedModel = event.target.value;
      setShowTextBox(selectedModel === "Custom");
      if (onModelChange) {
        onModelChange(selectedModel);
      }
    },
    [onModelChange]
  );

  const handleCustomModelChange = useCallback(
    (event: React.ChangeEvent<HTMLInputElement>) => {
      setCustomModel(event.target.value);
      if (showTextBox && onModelChange) {
        onModelChange(event.target.value);
      }
    },
    [showTextBox, onModelChange]
  );

  return (
    <div className="d-flex flex-column align-items-center justify-content-center mt-5">
      <i className="bi bi-chat-dots mb-3" style={{ fontSize: "50px" }}></i>
      <h4>No Messages Yet</h4>
      <p>
        Looks like there are no messages here. Start a conversation or check
        back later!
      </p>
      {onModelChange ? (
        <div>
          <select
            className="form-select"
            onChange={handleModelChange}
            defaultValue={modelName}
          >
            {MODEL_NAMES.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
            <option value="Custom">Custom (beta)</option>
          </select>
          {showTextBox && (
            <input
              type="text"
              className="form-control mt-3"
              value={customModel}
              onChange={handleCustomModelChange}
              placeholder="Enter custom model name"
            />
          )}
        </div>
      ) : null}
    </div>
  );
};

export default EmptyState;
