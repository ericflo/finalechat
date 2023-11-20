"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

export interface SearchBarProps {
  onNewChat: () => void;
}

const SearchBar = ({ onNewChat }: SearchBarProps) => (
  <div className="p-2 d-flex">
    <input
      type="text"
      className="form-control me-2"
      placeholder="Chat search coming soon..."
      disabled
    />
    <button className="btn btn-outline-primary" onClick={onNewChat}>
      <i className="bi-plus"></i>
    </button>
  </div>
);

export default SearchBar;
