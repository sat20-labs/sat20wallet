export const createPopup = async (url: string): Promise<void> => {
  const popup = window.open(url, '_blank', 'noopener,noreferrer');
  if (!popup) {
    window.location.href = url;
  }
};
