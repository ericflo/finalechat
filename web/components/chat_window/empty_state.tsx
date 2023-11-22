import React, { useCallback } from "react";

import { MODEL_NAMES } from "../../constants";

export interface EmptyStateProps {
  modelName?: string;
  onModelChange?: (modelName: string) => void;
}

const EmptyState = ({ modelName, onModelChange }: EmptyStateProps) => {
  const handleModelChange = useCallback(
    (event: React.ChangeEvent<HTMLSelectElement>) => {
      if (onModelChange) {
        onModelChange(event.target.value);
      }
    },
    [onModelChange]
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
        <select className="form-select" onChange={handleModelChange}>
          {MODEL_NAMES.map((name) => (
            <option key={name} value={name} selected={name == modelName}>
              {name}
            </option>
          ))}
        </select>
      ) : null}
    </div>
  );
};

export default EmptyState;
