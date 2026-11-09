function node(tag, value, className) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (value !== undefined && value !== null) element.textContent = String(value);
  return element;
}

export function mount(root, context) {
  const render = (current) => {
    const section = node("section", null, "qa-catalog");
    section.append(node("h1", current.arguments.heading));
    section.append(node("p", current.arguments.variant + " " + current.arguments.tags.join(", ")));
    const list = node("ul");
    for (const category of current.content.categories ?? []) {
      const group = node("li", category.name);
      const products = node("ul");
      for (const product of category.products ?? []) {
        products.append(node("li", product.title + " — " + product.price));
      }
      group.append(products);
      list.append(group);
    }
    section.append(list);
    section.append(node("span", current.content.currency));
    section.append(node("span", current.arguments.badgeImage, "qa-badge"));
    section.dataset.pageSize = String(current.arguments.pageSize);
    section.dataset.showPrices = String(current.arguments.showPrices);
    root.replaceChildren(section);
  };
  render(context);
  return { ready: Promise.resolve(), update: render, unmount() { root.replaceChildren(); } };
}
