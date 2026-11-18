// downloadFile makes the browser save text as a file named name
export function downloadFile(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/yaml' }));
  const link = document.createElement('a');
  link.href = url;
  link.download = name;
  link.click();
  // the browser may still be reading the blob once click returns
  setTimeout(() => URL.revokeObjectURL(url));
}
