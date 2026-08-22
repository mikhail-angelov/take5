// Extracts the *visual* metadata the Director needs about an action target.
//
// Deliberately never reads user-entered values (spec 12.4 / 27): the label sources below
// are author-supplied UI text only. `<input>.value` is read solely for button-like inputs,
// where the value attribute is the button's caption rather than user content.

(() => {
  const LABEL_MAX = 80;

  const ROLE_BY_TAG = {
    a: "link",
    button: "button",
    select: "combobox",
    textarea: "textbox",
    summary: "button",
    option: "option",
  };

  const ROLE_BY_INPUT_TYPE = {
    button: "button",
    submit: "button",
    reset: "button",
    image: "button",
    checkbox: "checkbox",
    radio: "radio",
    range: "slider",
    file: "button",
  };

  const INTERACTIVE_SELECTOR =
    "a,button,input,select,textarea,label,summary,option,[role],[onclick],[tabindex],[contenteditable]";

  function collapse(text) {
    return String(text || "").replace(/\s+/g, " ").trim();
  }

  function truncate(text) {
    return text.length > LABEL_MAX ? `${text.slice(0, LABEL_MAX - 1)}…` : text;
  }

  function roleOf(el) {
    const explicit = el.getAttribute && el.getAttribute("role");
    if (explicit) return collapse(explicit).split(" ")[0];

    const tag = el.tagName ? el.tagName.toLowerCase() : "";
    if (tag === "input") {
      const type = (el.getAttribute("type") || "text").toLowerCase();
      return ROLE_BY_INPUT_TYPE[type] || "textbox";
    }
    if (el.isContentEditable) return "textbox";
    return ROLE_BY_TAG[tag] || tag;
  }

  // Label sources in the preference order given by spec 12.3.
  function labelOf(el) {
    const aria = el.getAttribute && el.getAttribute("aria-label");
    if (collapse(aria)) return collapse(aria);

    const labelledBy = el.getAttribute && el.getAttribute("aria-labelledby");
    if (labelledBy) {
      const text = labelledBy
        .split(/\s+/)
        .map((id) => {
          const node = el.ownerDocument.getElementById(id);
          return node ? node.textContent : "";
        })
        .join(" ");
      if (collapse(text)) return collapse(text);
    }

    if (el.labels && el.labels.length) {
      const text = collapse(el.labels[0].textContent);
      if (text) return text;
    }

    const tag = el.tagName ? el.tagName.toLowerCase() : "";
    if (tag === "input") {
      const type = (el.getAttribute("type") || "text").toLowerCase();
      if (ROLE_BY_INPUT_TYPE[type] === "button") {
        const value = collapse(el.getAttribute("value"));
        if (value) return value;
      }
    }

    for (const attr of ["title", "alt", "placeholder", "name"]) {
      const value = collapse(el.getAttribute && el.getAttribute(attr));
      if (value) return value;
    }

    // innerText rather than textContent so hidden markup does not leak into captions.
    const text = collapse(el.innerText);
    if (text) return text;

    return "";
  }

  function rectOf(el) {
    const r = el.getBoundingClientRect();
    return {
      x: Math.round(r.left),
      y: Math.round(r.top),
      width: Math.round(r.width),
      height: Math.round(r.height),
    };
  }

  // Clicking an icon inside a button should describe the button, not the icon.
  function interactiveAncestor(node) {
    if (!node || node.nodeType !== 1) return null;
    const closest = node.closest && node.closest(INTERACTIVE_SELECTOR);
    return closest || node;
  }

  function describeTarget(node) {
    const el = interactiveAncestor(node);
    if (!el || !el.getBoundingClientRect) return null;

    const target = { role: roleOf(el), rect: rectOf(el) };
    const label = labelOf(el);
    if (label) target.label = truncate(label);
    return target;
  }

  globalThis.__demoRecorderTargetMetadata = { describeTarget, LABEL_MAX };
})();
