"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useState, useEffect, useCallback } from "react";
import SearchBar from "./search_bar";
import ChatList from "./chat_list";
import UserProfileSection from "./user_profile_section";

export interface Chat {
  id: number;
  title: string;
  // Add more chat-related properties here
}

// Temporary for demo purposes
let GLOBAL_CHAT_ID = 1;

async function fetchChats(): Promise<Chat[]> {
  // Replace with actual data fetching logic
  return [
    { id: GLOBAL_CHAT_ID++, title: "Chat 1" },
    { id: GLOBAL_CHAT_ID++, title: "Chat 2" },
    { id: GLOBAL_CHAT_ID++, title: "Chat 3" },
    { id: GLOBAL_CHAT_ID++, title: "Chat 4" },
  ];
}

// This component is intended to be the side panel of the chatbot application, it is
// responsible for rendering the list of chats and options and actions.
// The top item should be a search bar and a new chat icon
// After that, a section for a list of chats, which should expand to fill the available
// vertical space, and have a scrollbar if necessary, along with infinite scroll to load
// more chats as the user scrolls down.
// At the bottom, there should be a user profile section which, when tapped or clicked,
// opens a menu to go to settings, logout, or other user-related actions.
export default function SidePanel() {
  const [chats, setChats] = useState<Chat[]>([]);
  const [isLoading, setIsLoading] = useState(false);

  const loadMoreChats = useCallback(async () => {
    setIsLoading(true);
    try {
      // Replace with actual data fetching logic
      const newChats = await fetchChats();
      setChats((prevChats) => [...prevChats, ...newChats]);
    } catch (error) {
      // Handle error appropriately
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    loadMoreChats();
  }, [loadMoreChats]);

  return (
    <div className="d-flex flex-column h-100">
      <SearchBar />
      <div className="flex-grow-1 overflow-auto">
        <ChatList chats={chats} isLoading={isLoading} />
      </div>
      <div className="mt-auto">
        <UserProfileSection />
      </div>
    </div>
  );
}
