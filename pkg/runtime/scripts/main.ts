import { mountRuntime } from "./RuntimeApp";
import "../styles/public-runtime.css";

let root = document.getElementById("root");
if (!root) throw new Error("Public runtime root element is missing");

// A successful prerender keeps the populated page as #root in the HTTP
// document. Move only after JavaScript starts, then mount the existing feature
// ABI into its hidden live host while the server-rendered tree stays visible.
const liveRoot = document.getElementById("engine-live-root");
if (liveRoot) {
  root.id = "prerender-root";
  liveRoot.id = "root";
  root = liveRoot;
}

mountRuntime(root);
