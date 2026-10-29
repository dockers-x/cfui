# react-animals

![React Animals](./assets/hero.png)

A React component to add customizable animal avatars to any project. Inspired by the Google Docs "Anonymous Animals", all images designed by [Jefferson Cheng](https://jeffersoncheng.com/Anonymous-Animals) and property of Google. Images from Github repo by [wayou](https://github.com/wayou/anonymous-animals).

## Installation Process

Install from NPM:

```
npm install react-animals
```

Register the component globally (inside `main.js`):

```javascript
import Animal from "react-animals";
```

Once you've successfully imported the component, simply add it to a file:

```html
<Animal />
```

By default, this avatar is circular, 70px wide, and has a random color and animal. The available customizations are below.

## Select an Animal

![Animals](./assets/animals.png)

To specify an animal, use the `name` prop:

```html
<Animal name="alligator" />
```

If you provide an invalid name, a random name will be used by default and a console error will be thrown. A full list of animals is available [here](https://medium.com/@roeder/these-are-all-of-the-anonymous-google-docs-animals-i-have-seen-so-far-8e33e0405993).

## Select a Color

![Colors](./assets/colors.png)

To specify an color, use the `color` prop:

```html
<!--Use a predefined color-->
<Animal color="red" />
<!--Use a hex-code -->
<Animal color="#856EE7" />
```

For colors, you may use pre-selected colors like `red, orange, yellow, green, purple, teal` (all dark-mode friendly), or you may use `none` for a transparent background. You can also provide a valid CSS hexcode as well. If you provide an invalid color, a random color will be used by default and a console error will be thrown.

## Select a Size

![Sizes](./assets/sizes.png)

To specify a size, use the `size` prop:

```html
<Animal size="100px" />
<Animal size="10vw" />
<Animal size="25%" />
```

Although pixel (px units) are preferred, this component supports any valid CSS width unit. By design, the height and width of the avatar will be the same. If you provide an invalid CSS width, a standard width of `70px` will be used instead and a console error will be thrown. The max-width and height are 200px (to preserve image quality)

## Select a Shape

![Sizes](./assets/shapes.png)

To specify a shape, you'll need to provide a particular shape prop:

```html
<!--Use default circular shape, no prop needed-->
<Animal />
<!--Use a rounded square -->
<Animal rounded />
<!--Use a normal square -->
<Animal square />
```

If you provide both a `square` and `rounded` prop, the `rounded` option will be preferred. By default, all avatars will be circular.

## Dance!

![Dance](./assets/dance.gif)

To animate an avatar, use the `dance` prop:

```html
<!--Use default circular shape, no prop needed-->
<Animal dance />
```

This will make the avatar do a repeated dancing animation. You can apply this prop dynamically so that an avatar selectively dances (such as when a cursor is hovering over it).

## All customizations

Below are some examples of react-animals that use multiple customization props at once:

```html
<!--Alligator image, rounded square shape, random color, 70px size-->
<Animal name="alligator" rounded />

<!--Elephant image, square shape, blue color, 50px size-->
<Animal name="elephant" color="blue" size="50px" square />

<!--Animated dolphin image, circular shape, orange color, 80px size-->
<Animal name="dolphin" color="orange" size="80px" dance />
```

Lastly, all props can be dynamically generated using computed properties

```html
<Animal name="{animalVar}" rounded="{isRounded}" />
```
