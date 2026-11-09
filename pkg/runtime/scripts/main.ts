import { mountRuntime } from "./RuntimeApp";
import "../styles/public-runtime.css";

const root = document.getElementById("root");
if (!root) throw new Error("Public runtime root element is missing");
mountRuntime(root);
