function node(tag, value, className) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (value !== undefined && value !== null) element.textContent = String(value);
  return element;
}

export function mount(root, context) {
  const render = (current) => {
    const article = node("article", null, "qa-product-detail");
    article.append(node("h1", current.content.product?.title ?? "Missing product"));
    article.append(node("p", current.content.product?.category?.name ?? "Missing category"));
    article.append(node("data", current.content.product?.price ?? ""));
    article.dataset.slug = current.arguments.slug;
    root.replaceChildren(article);
  };
  render(context);
  return { ready: Promise.resolve(), update: render, unmount() { root.replaceChildren(); } };
}
