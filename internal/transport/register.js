'use strict';

const form = document.getElementById('register-form');
const submit = document.getElementById('submit');
const error = document.getElementById('form-error');
let membership;

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  if (submit.disabled) return;
  error.hidden = true;
  const name = form.elements.name.value.trim();
  const email = form.elements.email.value.trim();
  if (!name || new TextEncoder().encode(name).length > 200 || new TextEncoder().encode(email).length > 254) {
    error.textContent = 'Please enter your name and shorten any unusually long name or email address.';
    error.hidden = false;
    return;
  }
  submit.disabled = true;
  submit.textContent = 'Creating your membership…';
  form.setAttribute('aria-busy', 'true');
  try {
    const response = await fetch(form.action, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'omit',
      body: JSON.stringify({ name, email }),
    });
    if (!response.ok) {
      const messages = {
        400: 'Please check your name and email address and try again.',
        409: 'This email is already registered. Use your saved membership access key. If you have lost it, contact the membership team; account recovery is currently unavailable.',
        429: 'Too many attempts. Please wait a moment and try again.',
      };
      throw new Error(messages[response.status] || 'We couldn’t create your membership right now. Please try again later.');
    }
    membership = await response.json();
    document.getElementById('member-name').textContent = membership.customer.name;
    document.getElementById('member-email').textContent = membership.customer.email;
    document.getElementById('member-id').textContent = membership.customer.id;
    document.getElementById('member-points').textContent = membership.customer.points.toLocaleString();
    document.getElementById('access-key').textContent = membership.access_token;
    document.getElementById('registration').hidden = true;
    document.getElementById('confirmation').hidden = false;
    document.querySelector('.panel').setAttribute('aria-labelledby', 'success-title');
    document.getElementById('success-title').focus();
    document.title = 'Membership created · CenterLINECRM';
    form.reset();
  } catch (failure) {
    error.textContent = failure instanceof TypeError
      ? 'We couldn’t confirm registration. Check your connection. If a retry says your email is registered, contact the membership team.'
      : failure.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = 'Create my membership ↗';
    form.removeAttribute('aria-busy');
  }
});

document.getElementById('copy').addEventListener('click', async () => {
  const status = document.getElementById('save-status');
  try {
    await navigator.clipboard.writeText(membership.access_token);
    status.textContent = 'Access key copied. Store it somewhere private.';
  } catch {
    status.textContent = 'Copy is unavailable. Use “Show access key” to copy it manually, or download your details.';
  }
});

document.getElementById('download').addEventListener('click', () => {
  const contents = [
    'CenterLINECRM membership — keep this file private',
    `Service: ${new URL('../', window.location.href).href}`,
    `Name: ${membership.customer.name}`,
    `Email: ${membership.customer.email}`,
    `Member ID: ${membership.customer.id}`,
    `Access key: ${membership.access_token}`,
    '',
    'This key provides access to your membership. Account recovery is currently unavailable.',
  ].join('\n');
  const url = URL.createObjectURL(new Blob([contents], { type: 'text/plain;charset=utf-8' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = 'membership-details.txt';
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
  document.getElementById('save-status').textContent = 'Download requested. Keep your membership file somewhere private.';
});
