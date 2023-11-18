"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import SidePanel from "./side_panel";
import ChatWindow from "./chat_window";

// This is the root React component for the chatbot application, it is intended to be
// the sole content element on the page, taking over the entire viewport, and it is
// responsible for rendering the side panel which is a list of chats and options and actions,
// and the chat window for the selected chat including its input field.
export default function ChatBot() {
  return (
    <div className="vh-100 d-flex">
      <div className="col-3">
        <SidePanel />
      </div>
      <div className="col-9">
        <ChatWindow />
      </div>
    </div>
  );
}
