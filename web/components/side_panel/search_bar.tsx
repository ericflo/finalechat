"use client";
// Code Context: React TypeScript with bootstrap 5.3.2 and bootstrap-icons injected

const SearchBar = () => (
  <div className="p-2 d-flex">
    <input
      type="text"
      className="form-control me-2"
      placeholder="Search"
      disabled
    />
    <button className="btn btn-outline-primary">
      <i className="bi-plus"></i>
    </button>
  </div>
);

export default SearchBar;
