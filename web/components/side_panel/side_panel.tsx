"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useEffect, useCallback } from "react";
import SearchBar from "./search_bar";
import ChatList from "./chat_list";
import UserProfileSection from "./user_profile_section";
import { useGetChats } from "../../hooks/api";

export interface SidePanelProps {
  chatId: number;
  onChatSelected: (chatId: number) => void;
}

export default function SidePanel({ chatId, onChatSelected }: SidePanelProps) {
  const { getChats, chats, loading, error } = useGetChats();
  useEffect(() => {
    getChats();
  }, [chatId, getChats]);
  const handleNewChat = useCallback(() => onChatSelected(-1), [onChatSelected]);
  return (
    <div className="d-flex flex-column h-100">
      {error && <div className="alert alert-danger">{error}</div>}
      <SearchBar onNewChat={handleNewChat} />
      <div className="flex-grow-1 overflow-auto">
        <ChatList
          chats={chats?.items || []}
          isLoading={loading}
          onChatSelected={onChatSelected}
        />
      </div>
      <div className="mt-auto">
        <UserProfileSection />
      </div>
    </div>
  );
}
