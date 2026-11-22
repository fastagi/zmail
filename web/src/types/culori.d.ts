/** The few culori functions the theme contrast test uses; culori ships no types of its own. */
declare module 'culori' {
  export interface Color {
    mode: string;
    [channel: string]: number | string | undefined;
  }
  export interface Rgb extends Color {
    mode: 'rgb';
    r: number;
    g: number;
    b: number;
  }
  export function parse(color: string): Color | undefined;
  export function wcagContrast(a: Color | string, b: Color | string): number;
  export function converter(mode: 'rgb'): (color: Color | string | undefined) => Rgb | undefined;
}
