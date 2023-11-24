"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

import React, { useState, useCallback } from "react";
import { User } from "../../types";

export interface UserProfileProps {
  user: User;
  onLogout: () => void;
}

const UserProfileSection = ({ user, onLogout }: UserProfileProps) => {
  const [showMenu, setShowMenu] = useState(false);

  const toggleMenu = () => {
    setShowMenu(!showMenu);
  };

  const handleLogout = useCallback(() => {
    setShowMenu(false);
    onLogout();
  }, [setShowMenu, onLogout]);

  return (
    <div className="p-2 position-relative">
      <button className="btn btn-outline-secondary" onClick={toggleMenu}>
        <i className="bi-person-circle"></i>
      </button>

      {showMenu && user ? (
        <div
          className="card position-absolute bottom-100 start-40"
          style={{ zIndex: 1000 }}
        >
          <div className="card-body">
            <h6 className="card-title">{user.username}</h6>
            <p className="card-text">{user.email}</p>
            <div className="d-grid gap-2">
              <button className="btn btn-primary" onClick={handleLogout}>
                Logout
              </button>
              <a href="/settings" className="btn btn-secondary">
                Settings
              </a>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
};

export default UserProfileSection;
