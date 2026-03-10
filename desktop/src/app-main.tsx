import { StrictMode } from "react";
import ReactDOM from "react-dom/client";
import "./api/fetchOverride";
import MainApp from "./MainApp";
import "./style.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MainApp />
  </StrictMode>,
);
