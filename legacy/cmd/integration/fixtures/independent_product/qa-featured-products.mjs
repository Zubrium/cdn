function node(tag, value, className) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (value !== undefined && value !== null) element.textContent = String(value);
  return element;
}

export function mount(root, context) {
  const render = (current) => {
    const section = node("section", null, "qa-featured-products");
    section.append(node("h2", current.arguments.heading));
    const list = node("ol");
    for (const product of current.content.products ?? []) {
      list.append(node("li", product.title + " (" + product.slug + ")"));
    }
    section.append(list);
    root.replaceChildren(section);
  };
  render(context);
  return { ready: Promise.resolve(), update: render, unmount() { root.replaceChildren(); } };
}
