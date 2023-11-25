import React, { useState, useEffect } from "react";
import { Message } from "../../types";
import Markdown from "react-markdown";
import { useDeleteMessage, useUpdateMessage } from "../../hooks/api";
import { useAutoResizeTextarea } from "../../hooks/textareas";

export interface ChatMessageProps {
  message: Message;
  token: string;
  onMessageUpdate: () => void;
}

const ChatMessage: React.FC<ChatMessageProps> = ({
  message,
  token,
  onMessageUpdate,
}) => {
  const [isEditing, setIsEditing] = useState(false);
  const [editedText, setEditedText] = useState(message.text);
  const [showFeedbackBox, setShowFeedbackBox] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [voteType, setVoteType] = useState<"upvote" | "downvote">("upvote");
  const { deleteMessage } = useDeleteMessage(token);
  const { updateMessage } = useUpdateMessage(token);

  useEffect(() => {
    setEditedText(message.text);
  }, [message.text]);

  const handleDelete = async () => {
    try {
      if (!window.confirm("Are you sure you want to delete this message?")) {
        return;
      }
      await deleteMessage(message.id);
      onMessageUpdate();
    } catch (error) {
      console.error("Error deleting message: ", error);
      // Handle error (e.g., show a notification to the user)
    }
  };

  const handleEditSave = async () => {
    try {
      if (isEditing) {
        await updateMessage(message.id, editedText);
        onMessageUpdate();
      }
      setIsEditing(!isEditing);
    } catch (error) {
      console.error("Error updating message: ", error);
      // Handle error (e.g., show a notification to the user)
    }
  };

  const handleEditCancel = () => {
    setEditedText(message.text);
    setIsEditing(false);
  };

  const handleVoteClick = (type: "upvote" | "downvote") => {
    // Toggle feedback box and reset feedback if the same button is clicked again
    if (showFeedbackBox && voteType === type) {
      handleVoteCancel();
    } else {
      setVoteType(type);
      setShowFeedbackBox(true);
    }
  };

  const handleVoteSubmit = () => {
    console.log(`Vote: ${voteType}, Reason: ${feedback}`);
    // Add logic to handle vote submission
    setShowFeedbackBox(false);
    setFeedback("");
  };

  const handleVoteCancel = () => {
    setShowFeedbackBox(false);
    setFeedback("");
  };

  const editedTextareaRef = useAutoResizeTextarea(editedText);
  const feedbackTextareaRef = useAutoResizeTextarea(feedback);

  return (
    <div className="message py-2 my-1">
      <strong>{message.sender_type}</strong>
      <br />
      {isEditing ? (
        <>
          <textarea
            ref={editedTextareaRef}
            value={editedText}
            className="form-control"
            onChange={(e) => setEditedText(e.target.value)}
          />
          <div className="message-actions mt-2">
            <div
              className="btn-group"
              role="group"
              aria-label="Message actions"
            >
              <button
                className="btn btn-link text-decoration-none btn-action"
                onClick={handleEditCancel}
              >
                <i className="bi bi-x-lg"></i>
              </button>
              <button
                className="btn btn-link text-decoration-none btn-action"
                onClick={handleEditSave}
              >
                <i className="bi bi-save"></i>
              </button>
            </div>
          </div>
        </>
      ) : (
        <>
          <Markdown>{message.text}</Markdown>
          <div className="message-actions mt-2">
            <div
              className="btn-group"
              role="group"
              aria-label="Message actions"
            >
              {!showFeedbackBox && (
                <>
                  <button
                    className="btn btn-link text-decoration-none btn-action"
                    onClick={handleDelete}
                  >
                    <i className="bi bi-trash"></i>
                  </button>
                  <button
                    className="btn btn-link text-decoration-none btn-action"
                    onClick={handleEditSave}
                  >
                    <i className="bi bi-pencil"></i>
                  </button>
                </>
              )}
              <button
                className={
                  "btn btn-link text-decoration-none " +
                  (showFeedbackBox ? "" : "btn-action")
                }
                onClick={() => handleVoteClick("upvote")}
                disabled={showFeedbackBox && voteType === "downvote"}
              >
                <i className="bi bi-arrow-up-circle"></i>
              </button>
              <button
                className={
                  "btn btn-link text-decoration-none " +
                  (showFeedbackBox ? "" : "btn-action")
                }
                onClick={() => handleVoteClick("downvote")}
                disabled={showFeedbackBox && voteType === "upvote"}
              >
                <i className="bi bi-arrow-down-circle"></i>
              </button>
            </div>
          </div>
        </>
      )}

      {showFeedbackBox && (
        <div className="mt-2">
          <textarea
            ref={feedbackTextareaRef}
            value={feedback}
            onChange={(e) => setFeedback(e.target.value)}
            placeholder="Enter your reason here"
            className="form-control"
          />
          <div className="d-flex justify-content-end mt-2">
            <button
              onClick={handleVoteCancel}
              className="btn btn-link text-decoration-none btn-action"
            >
              <i className="bi bi-x-lg"></i>
            </button>
            <button
              onClick={handleVoteSubmit}
              className="btn btn-link text-decoration-none btn-action"
            >
              <i className="bi bi-check-lg"></i>
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

export default ChatMessage;
