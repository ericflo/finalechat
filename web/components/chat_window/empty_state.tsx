import React from "react";

const EmptyState = () => (
  <div className="d-flex flex-column align-items-center justify-content-center mt-5">
    <i className="bi bi-chat-dots mb-3" style={{ fontSize: "50px" }}></i>
    <h4>No Messages Yet</h4>
    <p>
      Looks like there are no messages here. Start a conversation or check back
      later!
    </p>
  </div>
);

export default EmptyState;
