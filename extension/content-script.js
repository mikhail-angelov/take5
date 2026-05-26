const TOOLBAR_FRAME_ID = 'take5-toolbar-frame';

function injectToolbar() {
  if (document.getElementById(TOOLBAR_FRAME_ID)) {
    return;
  }

  const frame = document.createElement('iframe');
  frame.id = TOOLBAR_FRAME_ID;
  frame.title = 'Take5 toolbar';
  frame.src = chrome.runtime.getURL('toolbar.html');
  frame.setAttribute('aria-label', 'Take5 toolbar');
  frame.style.position = 'fixed';
  frame.style.right = '16px';
  frame.style.bottom = '16px';
  frame.style.width = '360px';
  frame.style.height = '560px';
  frame.style.border = '0';
  frame.style.zIndex = '2147483647';
  frame.style.background = 'transparent';
  frame.style.boxShadow = '0 18px 50px rgba(15, 23, 42, 0.28)';
  frame.style.borderRadius = '18px';
  frame.style.overflow = 'hidden';

  (document.body || document.documentElement).appendChild(frame);
}

injectToolbar();
